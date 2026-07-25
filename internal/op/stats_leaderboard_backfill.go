package op

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	statsLeaderboardBackfillVersion    = 1
	statsLeaderboardBackfillWindow     = 30 * 24 * time.Hour
	statsLeaderboardBackfillPage       = 200
	statsLeaderboardBoundaryLookback   = 24 * time.Hour
	statsLeaderboardFailureSaveTimeout = 5 * time.Second
)

var statsLeaderboardBackfillLock sync.Mutex

// StatsLeaderboardBackfill performs the bounded projection from relay_logs.
// The persisted cutoff keeps historical and live sources disjoint; rerunning
// rebuilds the same projection so a partial/failed prior run is repaired.
func StatsLeaderboardBackfill(ctx context.Context) {
	statsImportLock.RLock()
	defer statsImportLock.RUnlock()

	if ctx == nil {
		ctx = context.Background()
	}
	statsLeaderboardBackfillLock.Lock()
	defer statsLeaderboardBackfillLock.Unlock()

	if err := statsLeaderboardBackfill(ctx, statsLeaderboardLiveSince); err != nil {
		log.Warnf("stats leaderboard backfill failed: %v", err)
	}
}

// statsLeaderboardBackfill is split out for deterministic tests. cutoff is the
// process-start boundary: logs before it are historical, and live rows after it
// are never counted again by this projection.
func statsLeaderboardBackfill(ctx context.Context, cutoff int64) error {
	coverage, scanStart, durableHistory, err := prepareLeaderboardBackfill(ctx, cutoff)
	if err != nil {
		return err
	}

	rows, err := collectLeaderboardBackfillRows(ctx, scanStart, coverage.BackfillCutoff)
	if err != nil {
		return markLeaderboardBackfillFailed(&coverage, err)
	}
	coverage.Status = model.StatsLeaderboardCoverageDone
	// A disabled relay-log store can still contain old rows, but it cannot prove
	// a continuous history window. Keep the useful aggregates while exposing
	// incomplete coverage to the client.
	if durableHistory {
		coverage.EarliestEventAt = scanStart
	}
	coverage.CompletedAt = time.Now().Unix()
	if err := replaceLeaderboardBackfillRows(ctx, rows, coverage); err != nil {
		return markLeaderboardBackfillFailed(&coverage, err)
	}
	log.Infof("stats leaderboard backfill done: %d rows from relay_logs", len(rows))
	return nil
}

func prepareLeaderboardBackfill(ctx context.Context, cutoff int64) (model.StatsLeaderboardCoverage, int64, bool, error) {
	if cutoff <= 0 {
		cutoff = time.Now().Unix()
	}
	var coverage model.StatsLeaderboardCoverage
	result := db.GetDB().WithContext(ctx).First(&coverage, 1)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return coverage, 0, false, fmt.Errorf("read leaderboard coverage: %w", result.Error)
	}
	if coverage.Version == statsLeaderboardBackfillVersion && coverage.BackfillCutoff > 0 {
		cutoff = coverage.BackfillCutoff
	}
	scanStart, durableHistory, err := leaderboardBackfillScanStart(cutoff)
	if err != nil {
		return coverage, 0, false, fmt.Errorf("resolve leaderboard history window: %w", err)
	}
	coverage = model.StatsLeaderboardCoverage{
		ID:             1,
		Version:        statsLeaderboardBackfillVersion,
		Status:         model.StatsLeaderboardCoverageRunning,
		BackfillCutoff: cutoff,
	}
	if err := db.GetDB().WithContext(ctx).Save(&coverage).Error; err != nil {
		return coverage, 0, false, fmt.Errorf("mark leaderboard backfill running: %w", err)
	}
	return coverage, scanStart, durableHistory, nil
}

func collectLeaderboardBackfillRows(ctx context.Context, scanStart, cutoff int64) ([]model.StatsLeaderboardHourly, error) {
	aggregated := make(map[leaderboardHourlyKey]*model.StatsLeaderboardHourly)
	queryStart := scanStart - int64(statsLeaderboardBoundaryLookback/time.Second)
	if queryStart < 0 {
		queryStart = 0
	}
	lastTime, lastID := queryStart, int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := loadLeaderboardBackfillPage(ctx, queryStart, cutoff, lastTime, lastID)
		if err != nil {
			return nil, fmt.Errorf("scan relay_logs: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		last := batch[len(batch)-1]
		lastTime, lastID = last.Time, last.ID
		for _, row := range batch {
			eventAt := relayLogEventAt(row.Time, row.UseTime)
			if eventAt >= scanStart && eventAt < cutoff {
				addBackfillLog(aggregated, row, eventAt)
			}
		}
		if len(batch) < statsLeaderboardBackfillPage {
			break
		}
	}
	rows := make([]model.StatsLeaderboardHourly, 0, len(aggregated))
	for _, row := range aggregated {
		rows = append(rows, *row)
	}
	return rows, nil
}

func loadLeaderboardBackfillPage(ctx context.Context, queryStart, cutoff, lastTime, lastID int64) ([]leaderboardBackfillLogRow, error) {
	var batch []leaderboardBackfillLogRow
	err := db.GetDB().WithContext(ctx).
		Model(&model.RelayLog{}).
		Select("id,time,channel_id,channel_name,actual_model_name,request_model_name,input_tokens,output_tokens,cost,use_time,success,attempts").
		Where("time >= ? AND time < ?", queryStart, cutoff).
		Where("time > ? OR (time = ? AND id > ?)", lastTime, lastTime, lastID).
		Order("time ASC").
		Order("id ASC").
		Limit(statsLeaderboardBackfillPage).
		Find(&batch).Error
	return batch, err
}

func leaderboardBackfillScanStart(cutoff int64) (int64, bool, error) {
	scanStart := cutoff - int64(statsLeaderboardBackfillWindow/time.Second)
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return 0, false, err
	}
	if !enabled {
		return scanStart, false, nil
	}

	keepDays, err := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		return 0, false, err
	}
	if keepDays > 0 {
		retentionStart := cutoff - int64(time.Duration(keepDays)*24*time.Hour/time.Second)
		if retentionStart > scanStart {
			scanStart = retentionStart
		}
	}
	return scanStart, true, nil
}

// leaderboardBackfillLogRow deliberately omits request_content and
// response_content. Those columns can be megabytes per row and are irrelevant
// to the three aggregate dimensions.
type leaderboardBackfillLogRow struct {
	ID               int64
	Time             int64
	ChannelId        int
	ChannelName      string
	ActualModelName  string
	RequestModelName string
	InputTokens      int
	OutputTokens     int
	Cost             float64
	UseTime          int
	Success          bool
	Attempts         []model.ChannelAttempt `gorm:"serializer:json"`
}

func relayLogEventAt(startUnix int64, useTimeMS int) int64 {
	if useTimeMS <= 0 {
		return startUnix
	}
	return startUnix + int64(useTimeMS)/int64(time.Second/time.Millisecond)
}

type leaderboardBackfillDimension struct {
	typ  string
	key  string
	name string
}

func addBackfillLog(
	aggregated map[leaderboardHourlyKey]*model.StatsLeaderboardHourly,
	row leaderboardBackfillLogRow,
	eventAt int64,
) {
	requestModel := normalizeLeaderboardText(row.RequestModelName, "unknown")
	actualModel := backfillFinalModel(row, requestModel)
	channelID, channelName := backfillFinalChannel(row)
	metrics := leaderboardBackfillMetrics(row)

	for _, dimension := range leaderboardBackfillDimensions(requestModel, actualModel, channelID, channelName) {
		addLeaderboardBackfillDimension(aggregated, dimension, metrics, eventAt)
	}
}

func leaderboardBackfillMetrics(row leaderboardBackfillLogRow) model.StatsMetrics {
	return model.StatsMetrics{
		InputToken:  int64(row.InputTokens),
		OutputToken: int64(row.OutputTokens),
		// RelayLog stores only total cost. Keeping it in InputCost preserves
		// total-cost sorting without inventing an input/output split.
		InputCost:      row.Cost,
		WaitTime:       int64(row.UseTime),
		RequestSuccess: boolToInt64(row.Success),
		RequestFailed:  boolToInt64(!row.Success),
	}
}

func leaderboardBackfillDimensions(
	requestModel string,
	actualModel string,
	channelID int,
	channelName string,
) [3]leaderboardBackfillDimension {
	return [3]leaderboardBackfillDimension{
		{model.StatsLeaderboardDimensionModel, actualModel, actualModel},
		{model.StatsLeaderboardDimensionChannel, fmt.Sprintf("%d", channelID), channelName},
		{model.StatsLeaderboardDimensionGroup, requestModel, requestModel},
	}
}

func addLeaderboardBackfillDimension(
	aggregated map[leaderboardHourlyKey]*model.StatsLeaderboardHourly,
	dimension leaderboardBackfillDimension,
	metrics model.StatsMetrics,
	eventAt int64,
) {
	hour := int(eventAt / 3600)
	key := leaderboardHourlyKey{
		Hour:          hour,
		DimensionType: dimension.typ,
		DimensionKey:  dimension.key,
		Source:        model.StatsLeaderboardSourceBackfill,
	}
	entry := aggregated[key]
	if entry == nil {
		entry = &model.StatsLeaderboardHourly{
			Hour:          hour,
			DimensionType: dimension.typ,
			DimensionKey:  dimension.key,
			Source:        model.StatsLeaderboardSourceBackfill,
			DimensionName: dimension.name,
			Date:          time.Unix(eventAt, 0).Format("20060102"),
		}
		aggregated[key] = entry
	}
	entry.StatsMetrics.Add(metrics)
	if dimension.name != "" {
		entry.DimensionName = dimension.name
	}
	if eventAt > entry.LastRequestAt {
		entry.LastRequestAt = eventAt
	}
}

func backfillFinalModel(row leaderboardBackfillLogRow, requestModel string) string {
	if actual := normalizeLeaderboardText(row.ActualModelName, ""); actual != "" {
		return actual
	}
	for i := len(row.Attempts) - 1; i >= 0; i-- {
		attempt := row.Attempts[i]
		if attempt.Status != model.AttemptSuccess && attempt.Status != model.AttemptFailed {
			continue
		}
		if modelName := normalizeLeaderboardText(attempt.ModelName, ""); modelName != "" {
			return modelName
		}
	}
	return requestModel
}

func backfillFinalChannel(row leaderboardBackfillLogRow) (int, string) {
	if row.ChannelId != 0 || strings.TrimSpace(row.ChannelName) != "" {
		name := strings.TrimSpace(row.ChannelName)
		if name == "" {
			name = fmt.Sprintf("channel-%d", row.ChannelId)
		}
		return row.ChannelId, name
	}
	for i := len(row.Attempts) - 1; i >= 0; i-- {
		attempt := row.Attempts[i]
		if attempt.Status != model.AttemptSuccess && attempt.Status != model.AttemptFailed {
			continue
		}
		name := strings.TrimSpace(attempt.ChannelName)
		if name == "" {
			name = fmt.Sprintf("channel-%d", attempt.ChannelID)
		}
		return attempt.ChannelID, name
	}
	return 0, "unassigned"
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func replaceLeaderboardBackfillRows(ctx context.Context, rows []model.StatsLeaderboardHourly, coverage model.StatsLeaderboardCoverage) error {
	leaderboardHourlyFlushLock.Lock()
	defer leaderboardHourlyFlushLock.Unlock()

	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source = ?", model.StatsLeaderboardSourceBackfill).Delete(&model.StatsLeaderboardHourly{}).Error; err != nil {
			return fmt.Errorf("clear old backfill rows: %w", err)
		}
		for start := 0; start < len(rows); start += 500 {
			end := start + 500
			if end > len(rows) {
				end = len(rows)
			}
			batch := rows[start:end]
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "hour"}, {Name: "dimension_type"}, {Name: "dimension_key"}, {Name: "source"},
				},
				UpdateAll: true,
			}).Create(&batch).Error; err != nil {
				return fmt.Errorf("write backfill rows: %w", err)
			}
		}
		if err := tx.Save(&coverage).Error; err != nil {
			return fmt.Errorf("save leaderboard coverage: %w", err)
		}
		return nil
	})
}

func markLeaderboardBackfillFailed(coverage *model.StatsLeaderboardCoverage, err error) error {
	coverage.Status = model.StatsLeaderboardCoverageFailed
	coverage.CompletedAt = 0
	saveCtx, cancel := context.WithTimeout(context.Background(), statsLeaderboardFailureSaveTimeout)
	defer cancel()
	if saveErr := db.GetDB().WithContext(saveCtx).Save(coverage).Error; saveErr != nil {
		return fmt.Errorf("%w; additionally mark failed: %v", err, saveErr)
	}
	return err
}
