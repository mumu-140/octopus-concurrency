package op

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/log"
	"gorm.io/gorm/clause"
)

const (
	statsSiteModelBackfillWindow = 30 * 24 * time.Hour
	statsSiteModelBackfillPage   = 200
	statsSiteModelBackfillChunk  = 500
)

type siteModelBackfillKey struct {
	Hour          int
	SiteAccountID int
	GroupKey      string
	ModelName     string
}

type siteModelBackfillWindow struct {
	scanStart  int64
	queryStart int64
	scanEnd    int64
}

// StatsSiteModelBackfill 一次性从最近的 relay_logs 回填 StatsSiteModelHourly 聚合表，
// 让首次启用此功能的实例也能立即看到历史折线图。已回填则跳过。
func StatsSiteModelBackfill(ctx context.Context) {
	statsImportLock.RLock()
	defer statsImportLock.RUnlock()

	ctx = normalizeBackfillContext(ctx)
	done, err := SettingGetBool(model.SettingKeyStatsSiteModelBackfilled)
	if err == nil && done {
		return
	}

	startedAt := time.Now()
	window := newSiteModelBackfillWindow(statsLeaderboardLiveSince)
	bindings, err := loadSiteModelBackfillBindings(ctx)
	if err != nil {
		log.Warnf("stats site model backfill: %v", err)
		return
	}

	rows, err := collectSiteModelBackfillRows(ctx, bindings, window)
	if err != nil {
		log.Warnf("stats site model backfill: %v", err)
		return
	}
	if err := saveSiteModelBackfillRows(ctx, rows); err != nil {
		log.Warnf("stats site model backfill: %v", err)
		return
	}
	if err := SettingSetString(model.SettingKeyStatsSiteModelBackfilled, "true"); err != nil {
		log.Warnf("stats site model backfill: failed to mark complete: %v", err)
		return
	}

	log.Infof(
		"stats site model backfill done: %d aggregated rows from %d-day window in %s",
		len(rows),
		int(statsSiteModelBackfillWindow/(24*time.Hour)),
		time.Since(startedAt),
	)
}

func normalizeBackfillContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func newSiteModelBackfillWindow(scanEnd int64) siteModelBackfillWindow {
	scanStart := scanEnd - int64(statsSiteModelBackfillWindow/time.Second)
	queryStart := scanStart - int64(statsLeaderboardBoundaryLookback/time.Second)
	if queryStart < 0 {
		queryStart = 0
	}
	return siteModelBackfillWindow{
		scanStart:  scanStart,
		queryStart: queryStart,
		scanEnd:    scanEnd,
	}
}

func loadSiteModelBackfillBindings(ctx context.Context) (map[int]channelSiteBinding, error) {
	var rows []model.SiteChannelBinding
	if err := db.GetDB().WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to load bindings: %w", err)
	}

	bindings := make(map[int]channelSiteBinding, len(rows))
	for _, row := range rows {
		baseGroupKey, _ := model.ParseSiteChannelBindingKey(row.GroupKey)
		bindings[row.ChannelID] = channelSiteBinding{
			SiteAccountID: row.SiteAccountID,
			BaseGroupKey:  baseGroupKey,
			Found:         true,
		}
	}
	return bindings, nil
}

func collectSiteModelBackfillRows(
	ctx context.Context,
	bindings map[int]channelSiteBinding,
	window siteModelBackfillWindow,
) ([]model.StatsSiteModelHourly, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	aggregated := make(map[siteModelBackfillKey]*model.StatsSiteModelHourly)
	lastTime, lastID := window.queryStart, int64(0)

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("canceled: %w", err)
		}
		batch, err := loadSiteModelBackfillPage(ctx, window.queryStart, window.scanEnd, lastTime, lastID)
		if err != nil {
			return nil, fmt.Errorf("scan logs failed: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		last := batch[len(batch)-1]
		lastTime, lastID = last.Time, last.ID
		addSiteModelBackfillBatch(aggregated, bindings, batch, window)
		if len(batch) < statsSiteModelBackfillPage {
			break
		}
	}

	rows := make([]model.StatsSiteModelHourly, 0, len(aggregated))
	for _, row := range aggregated {
		rows = append(rows, *row)
	}
	return rows, nil
}

func loadSiteModelBackfillPage(
	ctx context.Context,
	queryStart int64,
	scanEnd int64,
	lastTime int64,
	lastID int64,
) ([]backfillLogRow, error) {
	var batch []backfillLogRow
	err := db.GetDB().WithContext(ctx).
		Model(&model.RelayLog{}).
		Select("id,time,channel_id,actual_model_name,request_model_name,use_time,success,attempts").
		Where("time >= ? AND time < ?", queryStart, scanEnd).
		Where("time > ? OR (time = ? AND id > ?)", lastTime, lastTime, lastID).
		Order("time ASC").
		Order("id ASC").
		Limit(statsSiteModelBackfillPage).
		Find(&batch).Error
	return batch, err
}

func addSiteModelBackfillBatch(
	aggregated map[siteModelBackfillKey]*model.StatsSiteModelHourly,
	bindings map[int]channelSiteBinding,
	batch []backfillLogRow,
	window siteModelBackfillWindow,
) {
	for _, row := range batch {
		eventAt := relayLogEventAt(row.Time, row.UseTime)
		if eventAt < window.scanStart || eventAt >= window.scanEnd {
			continue
		}
		addSiteModelBackfillLog(aggregated, bindings, row, eventAt)
	}
}

func addSiteModelBackfillLog(
	aggregated map[siteModelBackfillKey]*model.StatsSiteModelHourly,
	bindings map[int]channelSiteBinding,
	row backfillLogRow,
	eventAt int64,
) {
	if len(row.Attempts) == 0 {
		modelName := firstSiteModelBackfillName(row.ActualModelName, row.RequestModelName)
		addSiteModelBackfillAttempt(aggregated, bindings, eventAt, row.ChannelId, modelName, row.Success)
		return
	}

	for _, attempt := range row.Attempts {
		if attempt.Status != model.AttemptSuccess && attempt.Status != model.AttemptFailed {
			continue
		}
		modelName := firstSiteModelBackfillName(
			attempt.ModelName,
			row.ActualModelName,
			row.RequestModelName,
		)
		addSiteModelBackfillAttempt(
			aggregated,
			bindings,
			eventAt,
			attempt.ChannelID,
			modelName,
			attempt.Status == model.AttemptSuccess,
		)
	}
}

func firstSiteModelBackfillName(names ...string) string {
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return ""
}

func addSiteModelBackfillAttempt(
	aggregated map[siteModelBackfillKey]*model.StatsSiteModelHourly,
	bindings map[int]channelSiteBinding,
	eventAt int64,
	channelID int,
	modelName string,
	success bool,
) {
	binding, ok := bindings[channelID]
	if !ok || modelName == "" {
		return
	}

	key := siteModelBackfillKey{
		Hour:          int(eventAt / 3600),
		SiteAccountID: binding.SiteAccountID,
		GroupKey:      binding.BaseGroupKey,
		ModelName:     modelName,
	}
	entry := aggregated[key]
	if entry == nil {
		entry = &model.StatsSiteModelHourly{
			Hour:          key.Hour,
			SiteAccountID: key.SiteAccountID,
			GroupKey:      key.GroupKey,
			ModelName:     key.ModelName,
			Date:          time.Unix(eventAt, 0).Format("20060102"),
		}
		aggregated[key] = entry
	}

	if success {
		entry.RequestSuccess++
	} else {
		entry.RequestFailed++
	}
	if eventAt > entry.LastRequestAt {
		entry.LastRequestAt = eventAt
	}
}

func saveSiteModelBackfillRows(ctx context.Context, rows []model.StatsSiteModelHourly) error {
	dbConn := db.GetDB().WithContext(ctx)
	for start := 0; start < len(rows); start += statsSiteModelBackfillChunk {
		end := start + statsSiteModelBackfillChunk
		if end > len(rows) {
			end = len(rows)
		}
		if err := dbConn.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "hour"},
				{Name: "site_account_id"},
				{Name: "group_key"},
				{Name: "model_name"},
			},
			DoNothing: true,
		}).Create(rows[start:end]).Error; err != nil {
			return fmt.Errorf("insert chunk failed: %w", err)
		}
	}
	return nil
}

// backfillLogRow 是一次性回填扫描专用的精简行结构。字段集合刻意比
// model.RelayLog 小，GORM 会按目的结构的字段裁剪 SELECT 列表，
// 把 request_content / response_content 等大字段留在数据库里。
// 不要给它增加内容字段，否则会重新引入 OOM 风险。
type backfillLogRow struct {
	ID               int64
	Time             int64
	ChannelId        int
	ActualModelName  string
	RequestModelName string
	UseTime          int
	Success          bool
	Attempts         []model.ChannelAttempt `gorm:"serializer:json"`
}
