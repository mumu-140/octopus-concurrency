package op

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

type StatsLeaderboardWindow string

const (
	StatsLeaderboardWindowToday  StatsLeaderboardWindow = "today"
	StatsLeaderboardWindow7Days  StatsLeaderboardWindow = "7"
	StatsLeaderboardWindow30Days StatsLeaderboardWindow = "30"
	StatsLeaderboardWindowAll    StatsLeaderboardWindow = "all"
)

type StatsLeaderboardRow struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	LastRequestAt int64  `json:"last_request_at"`
	model.StatsMetrics
}

type StatsLeaderboardCoverageView struct {
	Status          string `json:"status"`
	Complete        bool   `json:"complete"`
	EarliestEventAt int64  `json:"earliest_event_at"`
	BackfillCutoff  int64  `json:"backfill_cutoff"`
	CompletedAt     int64  `json:"completed_at"`
}

type StatsLeaderboardResult struct {
	Rows     []StatsLeaderboardRow        `json:"rows"`
	Coverage StatsLeaderboardCoverageView `json:"coverage"`
}

// StatsLeaderboardQuery returns one row per dimension key for a bounded
// window. It merges persisted rows with the not-yet-flushed live snapshot, but
// never mixes the old .10 table or attempt-level channel counters.
func StatsLeaderboardQuery(
	ctx context.Context,
	dimensionType string,
	window StatsLeaderboardWindow,
) (StatsLeaderboardResult, error) {
	leaderboardHourlyFlushLock.RLock()
	defer leaderboardHourlyFlushLock.RUnlock()

	ctx = normalizeBackfillContext(ctx)
	if err := validateLeaderboardQuery(dimensionType, window); err != nil {
		return StatsLeaderboardResult{}, err
	}

	now := statsLeaderboardNow()
	rows, err := loadLeaderboardQueryRows(ctx, dimensionType, leaderboardWindowMinHour(window, now))
	if err != nil {
		return StatsLeaderboardResult{}, err
	}
	coverage, err := statsLeaderboardCoverage(ctx)
	if err != nil {
		return StatsLeaderboardResult{}, err
	}
	coverage.Complete = leaderboardCoverageComplete(coverage, window, now)
	return StatsLeaderboardResult{
		Rows:     aggregateLeaderboardQueryRows(rows),
		Coverage: coverage,
	}, nil
}

func validateLeaderboardQuery(dimensionType string, window StatsLeaderboardWindow) error {
	if !StatsLeaderboardDimensionValid(dimensionType) {
		return fmt.Errorf("invalid leaderboard dimension: %s", dimensionType)
	}
	if !StatsLeaderboardWindowValid(window) {
		return fmt.Errorf("invalid leaderboard window: %s", window)
	}
	return nil
}

func loadLeaderboardQueryRows(
	ctx context.Context,
	dimensionType string,
	minHour int,
) ([]model.StatsLeaderboardHourly, error) {
	query := db.GetDB().WithContext(ctx).Where("dimension_type = ?", dimensionType)
	if minHour > 0 {
		query = query.Where("hour >= ?", minHour)
	}
	var rows []model.StatsLeaderboardHourly
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}

	leaderboardHourlyCacheLock.Lock()
	defer leaderboardHourlyCacheLock.Unlock()
	for key, row := range leaderboardHourlyCache {
		if key.DimensionType == dimensionType && (minHour == 0 || key.Hour >= minHour) {
			rows = append(rows, *row)
		}
	}
	return rows, nil
}

func aggregateLeaderboardQueryRows(rows []model.StatsLeaderboardHourly) []StatsLeaderboardRow {
	aggregated := make(map[string]*StatsLeaderboardRow, len(rows))
	for _, row := range rows {
		current := aggregated[row.DimensionKey]
		if current == nil {
			current = &StatsLeaderboardRow{Key: row.DimensionKey, Name: row.DimensionName}
			aggregated[row.DimensionKey] = current
		}
		current.StatsMetrics.Add(row.StatsMetrics)
		if row.LastRequestAt >= current.LastRequestAt && row.DimensionName != "" {
			current.Name = row.DimensionName
		}
		if row.LastRequestAt > current.LastRequestAt {
			current.LastRequestAt = row.LastRequestAt
		}
	}

	result := make([]StatsLeaderboardRow, 0, len(aggregated))
	for _, row := range aggregated {
		if row.Name == "" {
			row.Name = row.Key
		}
		result = append(result, *row)
	}
	sortLeaderboardQueryRows(result)
	return result
}

func sortLeaderboardQueryRows(rows []StatsLeaderboardRow) {
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i].RequestSuccess + rows[i].RequestFailed
		right := rows[j].RequestSuccess + rows[j].RequestFailed
		if left != right {
			return left > right
		}
		return rows[i].Key < rows[j].Key
	})
}

func StatsLeaderboardDimensionValid(value string) bool {
	switch value {
	case model.StatsLeaderboardDimensionModel, model.StatsLeaderboardDimensionChannel, model.StatsLeaderboardDimensionGroup:
		return true
	default:
		return false
	}
}

func StatsLeaderboardWindowValid(value StatsLeaderboardWindow) bool {
	switch value {
	case StatsLeaderboardWindowToday, StatsLeaderboardWindow7Days, StatsLeaderboardWindow30Days, StatsLeaderboardWindowAll:
		return true
	default:
		return false
	}
}

func leaderboardWindowMinHour(window StatsLeaderboardWindow, now time.Time) int {
	local := now.In(now.Location())
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	var start time.Time
	switch window {
	case StatsLeaderboardWindowToday:
		start = dayStart
	case StatsLeaderboardWindow7Days:
		// The home chart uses the current day plus the preceding six calendar days.
		start = dayStart.AddDate(0, 0, -6)
	case StatsLeaderboardWindow30Days:
		// The home chart uses the current day plus the preceding 29 calendar days.
		start = dayStart.AddDate(0, 0, -29)
	default:
		return 0
	}
	return int(start.Unix() / 3600)
}

func statsLeaderboardCoverage(ctx context.Context) (StatsLeaderboardCoverageView, error) {
	var coverage model.StatsLeaderboardCoverage
	result := db.GetDB().WithContext(ctx).First(&coverage, 1)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return StatsLeaderboardCoverageView{Status: model.StatsLeaderboardCoveragePending}, nil
		}
		return StatsLeaderboardCoverageView{}, result.Error
	}
	return StatsLeaderboardCoverageView{
		Status:          coverage.Status,
		EarliestEventAt: coverage.EarliestEventAt,
		BackfillCutoff:  coverage.BackfillCutoff,
		CompletedAt:     coverage.CompletedAt,
	}, nil
}

func leaderboardCoverageComplete(
	coverage StatsLeaderboardCoverageView,
	window StatsLeaderboardWindow,
	now time.Time,
) bool {
	if coverage.Status != model.StatsLeaderboardCoverageDone || coverage.EarliestEventAt == 0 {
		return false
	}
	if window == StatsLeaderboardWindowAll {
		return false
	}
	startHour := leaderboardWindowMinHour(window, now)
	return coverage.EarliestEventAt <= int64(startHour)*3600
}
