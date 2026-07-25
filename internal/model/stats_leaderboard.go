package model

// StatsLeaderboardHourly stores additive hourly aggregates for the home-page
// leaderboard. Source separates deterministic relay-log backfill rows from
// live request accounting, so retries never collide with live traffic.
type StatsLeaderboardHourly struct {
	Hour          int    `json:"hour" gorm:"primaryKey;autoIncrement:false;index:idx_stats_leaderboard_lookup,priority:2"`
	DimensionType string `json:"dimension_type" gorm:"primaryKey;type:varchar(16);index:idx_stats_leaderboard_lookup,priority:1"`
	DimensionKey  string `json:"dimension_key" gorm:"primaryKey;type:varchar(256)"`
	Source        string `json:"source" gorm:"primaryKey;type:varchar(32)"`
	DimensionName string `json:"dimension_name" gorm:"not null;type:varchar(256)"`
	Date          string `json:"date" gorm:"not null;type:varchar(8)"`
	LastRequestAt int64  `json:"last_request_at" gorm:"not null;default:0"`
	StatsMetrics
}

// StatsLeaderboardCoverage describes the bounded relay-log history used to
// seed the leaderboard. It prevents a retained-log window from being presented
// as complete all-time history.
type StatsLeaderboardCoverage struct {
	ID              int    `json:"id" gorm:"primaryKey"`
	Version         int    `json:"version" gorm:"not null;default:0"`
	Status          string `json:"status" gorm:"not null;type:varchar(16);default:'pending'"`
	EarliestEventAt int64  `json:"earliest_event_at" gorm:"not null;default:0"`
	BackfillCutoff  int64  `json:"backfill_cutoff" gorm:"not null;default:0"`
	CompletedAt     int64  `json:"completed_at" gorm:"not null;default:0"`
}

const (
	StatsLeaderboardDimensionModel   = "model"
	StatsLeaderboardDimensionChannel = "channel"
	StatsLeaderboardDimensionGroup   = "group"

	StatsLeaderboardSourceLive     = "live"
	StatsLeaderboardSourceBackfill = "relay_logs_v1"

	StatsLeaderboardCoveragePending = "pending"
	StatsLeaderboardCoverageRunning = "running"
	StatsLeaderboardCoverageDone    = "completed"
	StatsLeaderboardCoverageFailed  = "failed"
)
