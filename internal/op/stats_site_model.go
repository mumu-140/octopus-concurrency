package op

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// channelSiteBinding 是 channelID → 站点账号绑定的简化形式。
type channelSiteBinding struct {
	SiteAccountID int
	BaseGroupKey  string
	Found         bool
}

// 站点渠道绑定缓存，懒加载，正向命中永久持有；负向命中也缓存，避免每次请求都查 DB。
// 由于 SiteChannelBinding 的 channel_id 是 uniqueIndex，迁移场景下绑定基本不会重映射，
// 在站点账号删除时会调用 invalidateSiteBindingCache 清理。
var siteBindingByChannelCache = cache.New[int, channelSiteBinding](16)

// 桶级缓存：以小时桶为粒度累加，由后台任务批量持久化。
type siteModelHourlyKey struct {
	Hour          int
	SiteAccountID int
	GroupKey      string
	ModelName     string
}

var siteModelHourlyCache = make(map[siteModelHourlyKey]*model.StatsSiteModelHourly)
var siteModelHourlyCacheLock sync.Mutex

// siteModelHourlyFlushLock keeps a query from observing the gap between
// detaching pending buckets and committing them to the database.
var siteModelHourlyFlushLock sync.RWMutex

// StatsSiteModelHourlyUpdate 记录一次站点渠道请求到对应小时桶。
// 非站点渠道（无绑定）会被静默忽略。
func StatsSiteModelHourlyUpdate(channelID int, actualModel string, metrics model.StatsMetrics) {
	statsImportLock.RLock()
	defer statsImportLock.RUnlock()
	siteModelHourlyFlushLock.RLock()
	defer siteModelHourlyFlushLock.RUnlock()

	actualModel = strings.TrimSpace(actualModel)
	if channelID == 0 || actualModel == "" {
		return
	}

	binding, err := lookupChannelSiteBinding(channelID)
	if err != nil || !binding.Found {
		return
	}

	now := time.Now()
	hour := int(now.Unix() / 3600)
	nowSec := now.Unix()
	date := now.Format("20060102")

	key := siteModelHourlyKey{
		Hour:          hour,
		SiteAccountID: binding.SiteAccountID,
		GroupKey:      binding.BaseGroupKey,
		ModelName:     actualModel,
	}

	siteModelHourlyCacheLock.Lock()
	defer siteModelHourlyCacheLock.Unlock()
	entry, ok := siteModelHourlyCache[key]
	if !ok {
		entry = &model.StatsSiteModelHourly{
			Hour:          hour,
			SiteAccountID: binding.SiteAccountID,
			GroupKey:      binding.BaseGroupKey,
			ModelName:     actualModel,
			Date:          date,
		}
		siteModelHourlyCache[key] = entry
	}
	entry.StatsMetrics.Add(metrics)
	if nowSec > entry.LastRequestAt {
		entry.LastRequestAt = nowSec
	}
}

// StatsSiteModelHourlyRecordAttempts 把一次 relay 中所有 success/failed attempts
// 按 (channel, attempt.modelName) 维度记录到小时桶。仅累加 request_success/request_failed，
// 与现有 site_channel 历史计数语义一致；token/cost 等不在此处累加（已由全局 stats 处理）。
func StatsSiteModelHourlyRecordAttempts(attempts []model.ChannelAttempt, fallbackModel string) {
	for _, attempt := range attempts {
		if attempt.ChannelID == 0 {
			continue
		}
		if attempt.Status != model.AttemptSuccess && attempt.Status != model.AttemptFailed {
			continue
		}
		modelName := strings.TrimSpace(attempt.ModelName)
		if modelName == "" {
			modelName = strings.TrimSpace(fallbackModel)
		}
		if modelName == "" {
			continue
		}
		var metrics model.StatsMetrics
		if attempt.Status == model.AttemptSuccess {
			metrics.RequestSuccess = 1
		} else {
			metrics.RequestFailed = 1
		}
		StatsSiteModelHourlyUpdate(attempt.ChannelID, modelName, metrics)
	}
}

// StatsSiteModelHourlySaveDB 把内存桶批量 upsert 入库。
// 由 stats 后台任务调用。
func StatsSiteModelHourlySaveDB(ctx context.Context) error {
	siteModelHourlyFlushLock.Lock()
	defer siteModelHourlyFlushLock.Unlock()

	siteModelHourlyCacheLock.Lock()
	if len(siteModelHourlyCache) == 0 {
		siteModelHourlyCacheLock.Unlock()
		return nil
	}
	snapshot := siteModelHourlyCache
	siteModelHourlyCache = make(map[siteModelHourlyKey]*model.StatsSiteModelHourly)
	siteModelHourlyCacheLock.Unlock()

	rows := make([]model.StatsSiteModelHourly, 0, len(snapshot))
	for _, entry := range snapshot {
		rows = append(rows, *entry)
	}

	dbConn := db.GetDB().WithContext(ctx)
	err := dbConn.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "hour"}, {Name: "site_account_id"}, {Name: "group_key"}, {Name: "model_name"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"date":            clause.Column{Name: "date"},
			"input_token":     gorm.Expr("stats_site_model_hourlies.input_token + EXCLUDED.input_token"),
			"output_token":    gorm.Expr("stats_site_model_hourlies.output_token + EXCLUDED.output_token"),
			"input_cost":      gorm.Expr("stats_site_model_hourlies.input_cost + EXCLUDED.input_cost"),
			"output_cost":     gorm.Expr("stats_site_model_hourlies.output_cost + EXCLUDED.output_cost"),
			"wait_time":       gorm.Expr("stats_site_model_hourlies.wait_time + EXCLUDED.wait_time"),
			"request_success": gorm.Expr("stats_site_model_hourlies.request_success + EXCLUDED.request_success"),
			"request_failed":  gorm.Expr("stats_site_model_hourlies.request_failed + EXCLUDED.request_failed"),
			"last_request_at": gorm.Expr("MAX(stats_site_model_hourlies.last_request_at, EXCLUDED.last_request_at)"),
		}),
	}).Create(&rows).Error
	if err == nil {
		return nil
	}

	// A failed write must not discard the detached snapshot. Updates cannot
	// enter while the flush barrier is held, but merge defensively so this stays
	// correct if the locking scope changes later.
	siteModelHourlyCacheLock.Lock()
	for key, row := range snapshot {
		if current, ok := siteModelHourlyCache[key]; ok {
			current.StatsMetrics.Add(row.StatsMetrics)
			if row.LastRequestAt > current.LastRequestAt {
				current.LastRequestAt = row.LastRequestAt
			}
		} else {
			copyRow := *row
			siteModelHourlyCache[key] = &copyRow
		}
	}
	siteModelHourlyCacheLock.Unlock()
	return err
}

// lookupChannelSiteBinding 查询并缓存 channelID → 站点绑定信息。
func lookupChannelSiteBinding(channelID int) (channelSiteBinding, error) {
	if cached, ok := siteBindingByChannelCache.Get(channelID); ok {
		return cached, nil
	}
	var binding model.SiteChannelBinding
	err := db.GetDB().Where("channel_id = ?", channelID).First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result := channelSiteBinding{Found: false}
			siteBindingByChannelCache.Set(channelID, result)
			return result, nil
		}
		return channelSiteBinding{}, err
	}
	baseGroupKey, _ := model.ParseSiteChannelBindingKey(binding.GroupKey)
	result := channelSiteBinding{
		SiteAccountID: binding.SiteAccountID,
		BaseGroupKey:  baseGroupKey,
		Found:         true,
	}
	siteBindingByChannelCache.Set(channelID, result)
	return result, nil
}

func deleteSiteModelHourlyCacheForAccounts(accountIDs []int) {
	if len(accountIDs) == 0 {
		return
	}
	accountSet := make(map[int]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		accountSet[id] = struct{}{}
	}

	siteModelHourlyCacheLock.Lock()
	defer siteModelHourlyCacheLock.Unlock()
	for key := range siteModelHourlyCache {
		if _, ok := accountSet[key.SiteAccountID]; ok {
			delete(siteModelHourlyCache, key)
		}
	}
}

// invalidateSiteBindingCache 在站点账号变更时清理映射缓存。
func invalidateSiteBindingCache() {
	siteBindingByChannelCache.Clear()
}
