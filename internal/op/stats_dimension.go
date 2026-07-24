package op

import (
	"context"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 维度小时桶缓存：以 (hour, dimensionType, dimensionKey) 为粒度累加，
// 由 stats 后台任务批量刷盘。镜像 stats_site_model.go 的桶级缓存写法。
type dimHourlyKey struct {
	Hour          int
	DimensionType string
	DimensionKey  string
}

var dimHourlyCache = make(map[dimHourlyKey]*model.StatsDimensionHourly)
var dimHourlyCacheLock sync.Mutex

// StatsDimensionHourlyUpdate 记录一次请求到对应维度的小时桶。
// dimKey 为空时静默忽略（例如无渠道命中或无请求分组名）。
func StatsDimensionHourlyUpdate(dimType, dimKey string, metrics model.StatsMetrics) {
	if dimType == "" || dimKey == "" {
		return
	}

	now := time.Now()
	hour := int(now.Unix() / 3600)
	nowSec := now.Unix()
	date := now.Format("20060102")

	key := dimHourlyKey{Hour: hour, DimensionType: dimType, DimensionKey: dimKey}

	dimHourlyCacheLock.Lock()
	defer dimHourlyCacheLock.Unlock()
	entry, ok := dimHourlyCache[key]
	if !ok {
		entry = &model.StatsDimensionHourly{
			Hour:          hour,
			DimensionType: dimType,
			DimensionKey:  dimKey,
			Date:          date,
		}
		dimHourlyCache[key] = entry
	}
	entry.StatsMetrics.Add(metrics)
	if nowSec > entry.LastRequestAt {
		entry.LastRequestAt = nowSec
	}
}

// StatsDimensionHourlySaveDB 把内存桶批量 upsert 入库，由 stats 后台任务调用。
// 与 site_model 一致，采用 lossy-on-error：刷盘失败时该周期增量丢弃，不阻塞后续统计。
// 排行榜为尽力而为的展示数据，可接受偶发丢桶。
func StatsDimensionHourlySaveDB(ctx context.Context) error {
	dimHourlyCacheLock.Lock()
	if len(dimHourlyCache) == 0 {
		dimHourlyCacheLock.Unlock()
		return nil
	}
	rows := make([]model.StatsDimensionHourly, 0, len(dimHourlyCache))
	for _, entry := range dimHourlyCache {
		rows = append(rows, *entry)
	}
	dimHourlyCache = make(map[dimHourlyKey]*model.StatsDimensionHourly)
	dimHourlyCacheLock.Unlock()

	dbConn := db.GetDB().WithContext(ctx)
	return dbConn.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "hour"}, {Name: "dimension_type"}, {Name: "dimension_key"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"date":            clause.Column{Name: "date"},
			"input_token":     gorm.Expr("stats_dimension_hourlies.input_token + EXCLUDED.input_token"),
			"output_token":    gorm.Expr("stats_dimension_hourlies.output_token + EXCLUDED.output_token"),
			"input_cost":      gorm.Expr("stats_dimension_hourlies.input_cost + EXCLUDED.input_cost"),
			"output_cost":     gorm.Expr("stats_dimension_hourlies.output_cost + EXCLUDED.output_cost"),
			"wait_time":       gorm.Expr("stats_dimension_hourlies.wait_time + EXCLUDED.wait_time"),
			"request_success": gorm.Expr("stats_dimension_hourlies.request_success + EXCLUDED.request_success"),
			"request_failed":  gorm.Expr("stats_dimension_hourlies.request_failed + EXCLUDED.request_failed"),
			"last_request_at": gorm.Expr("MAX(stats_dimension_hourlies.last_request_at, EXCLUDED.last_request_at)"),
		}),
	}).Create(&rows).Error
}

// DimensionRankRow 是排行榜聚合的单行结果：一个维度键的窗口内累计指标。
type DimensionRankRow struct {
	DimensionKey  string `json:"dimension_key"`
	LastRequestAt int64  `json:"last_request_at"`
	model.StatsMetrics
}

// StatsDimensionHourlyWindow 读取指定维度在 [now-lookback, now] 窗口内的聚合，
// 合并未刷盘内存桶后按 dimensionKey 汇总。lookback<=0 表示全部时间。
func StatsDimensionHourlyWindow(ctx context.Context, dimType string, lookback time.Duration) ([]DimensionRankRow, error) {
	minHour := 0
	if lookback > 0 {
		minHour = int(time.Now().Add(-lookback).Unix() / 3600)
	}

	var rows []model.StatsDimensionHourly
	if err := db.GetDB().WithContext(ctx).
		Where("dimension_type = ? AND hour >= ?", dimType, minHour).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	// 合并尚未刷盘的内存桶。
	dimHourlyCacheLock.Lock()
	for k, entry := range dimHourlyCache {
		if k.DimensionType == dimType && k.Hour >= minHour {
			rows = append(rows, *entry)
		}
	}
	dimHourlyCacheLock.Unlock()

	agg := make(map[string]*DimensionRankRow, len(rows))
	for _, r := range rows {
		row, ok := agg[r.DimensionKey]
		if !ok {
			row = &DimensionRankRow{DimensionKey: r.DimensionKey}
			agg[r.DimensionKey] = row
		}
		row.StatsMetrics.Add(r.StatsMetrics)
		if r.LastRequestAt > row.LastRequestAt {
			row.LastRequestAt = r.LastRequestAt
		}
	}

	result := make([]DimensionRankRow, 0, len(agg))
	for _, row := range agg {
		result = append(result, *row)
	}
	return result, nil
}
