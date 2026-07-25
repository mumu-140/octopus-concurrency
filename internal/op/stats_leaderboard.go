package op

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StatsLeaderboardEvent is the final, user-visible outcome of one request.
// It is intentionally distinct from ChannelAttempt: retries are not counted
// as extra user requests in the model/channel/group leaderboard.
type StatsLeaderboardEvent struct {
	APIKeyID     int
	RequestModel string
	ActualModel  string
	ChannelID    int
	ChannelName  string
	Metrics      model.StatsMetrics
}

type leaderboardHourlyKey struct {
	Hour          int
	DimensionType string
	DimensionKey  string
	Source        string
}

var leaderboardHourlyCache = make(map[leaderboardHourlyKey]*model.StatsLeaderboardHourly)
var leaderboardHourlyCacheLock sync.Mutex

// leaderboardHourlyFlushLock serializes a cache flush with readers. Without
// this barrier a reader could observe the cache after it was detached but
// before SQLite committed the detached rows, temporarily dropping a whole
// batch from the leaderboard (or double-counting it after commit).
var leaderboardHourlyFlushLock sync.RWMutex

// leaderboardFlushBeforeDBWrite is nil in production and is used by tests to
// hold the write barrier while asserting that readers cannot observe a detached
// cache before its SQLite transaction commits.
var leaderboardFlushBeforeDBWrite func()

// statsLeaderboardNow is replaceable in tests so window and bucket behavior is
// deterministic. Production always uses time.Now.
var statsLeaderboardNow = time.Now

// The process start boundary separates requests written live from the one-time
// historical projection. A retry reuses the persisted coverage cutoff.
var statsLeaderboardLiveSince = time.Now().Unix()

// RecordStatsEvent updates the existing global/API-key caches and the new
// dimension ledger together. Existing channel-attempt and site-model metrics
// remain owned by their original callers.
func RecordStatsEvent(ctx context.Context, event StatsLeaderboardEvent) {
	statsImportLock.RLock()
	defer statsImportLock.RUnlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := StatsTotalUpdate(event.Metrics); err != nil {
		log.Warnf("failed to update total stats: %v", err)
	}
	if err := StatsHourlyUpdate(event.Metrics); err != nil {
		log.Warnf("failed to update hourly stats: %v", err)
	}
	if err := StatsDailyUpdate(context.WithoutCancel(ctx), event.Metrics); err != nil {
		log.Warnf("failed to update daily stats: %v", err)
	}
	if err := StatsAPIKeyUpdate(event.APIKeyID, event.Metrics); err != nil {
		log.Warnf("failed to update API key stats: %v", err)
	}

	event.RequestModel = normalizeLeaderboardText(event.RequestModel, "unknown")
	event.ActualModel = normalizeLeaderboardText(event.ActualModel, event.RequestModel)
	if event.ChannelName == "" {
		if event.ChannelID == 0 {
			event.ChannelName = "unassigned"
		} else {
			event.ChannelName = fmt.Sprintf("channel-%d", event.ChannelID)
		}
	}
	recordLeaderboardEvent(event, statsLeaderboardNow())
}

func recordLeaderboardEvent(event StatsLeaderboardEvent, now time.Time) {
	leaderboardHourlyFlushLock.RLock()
	defer leaderboardHourlyFlushLock.RUnlock()

	hour := int(now.Unix() / 3600)
	date := now.Format("20060102")
	dimensions := [...]struct {
		typ  string
		key  string
		name string
	}{
		{model.StatsLeaderboardDimensionModel, event.ActualModel, event.ActualModel},
		{model.StatsLeaderboardDimensionChannel, strconv.Itoa(event.ChannelID), event.ChannelName},
		{model.StatsLeaderboardDimensionGroup, event.RequestModel, event.RequestModel},
	}

	leaderboardHourlyCacheLock.Lock()
	defer leaderboardHourlyCacheLock.Unlock()
	for _, dimension := range dimensions {
		key := leaderboardHourlyKey{
			Hour:          hour,
			DimensionType: dimension.typ,
			DimensionKey:  dimension.key,
			Source:        model.StatsLeaderboardSourceLive,
		}
		row := leaderboardHourlyCache[key]
		if row == nil {
			row = &model.StatsLeaderboardHourly{
				Hour:          hour,
				DimensionType: dimension.typ,
				DimensionKey:  dimension.key,
				Source:        model.StatsLeaderboardSourceLive,
				DimensionName: dimension.name,
				Date:          date,
			}
			leaderboardHourlyCache[key] = row
		}
		row.StatsMetrics.Add(event.Metrics)
		if dimension.name != "" {
			row.DimensionName = dimension.name
		}
		if now.Unix() > row.LastRequestAt {
			row.LastRequestAt = now.Unix()
		}
	}
}

// StatsLeaderboardHourlySaveDB flushes a snapshot of the live cache. New
// requests arriving while SQLite is busy stay in the live cache; failed writes
// merge the snapshot back so no request is silently lost.
func StatsLeaderboardHourlySaveDB(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	leaderboardHourlyFlushLock.Lock()
	defer leaderboardHourlyFlushLock.Unlock()

	leaderboardHourlyCacheLock.Lock()
	if len(leaderboardHourlyCache) == 0 {
		leaderboardHourlyCacheLock.Unlock()
		return nil
	}
	snapshot := leaderboardHourlyCache
	leaderboardHourlyCache = make(map[leaderboardHourlyKey]*model.StatsLeaderboardHourly)
	leaderboardHourlyCacheLock.Unlock()

	rows := make([]model.StatsLeaderboardHourly, 0, len(snapshot))
	for _, row := range snapshot {
		rows = append(rows, *row)
	}
	if leaderboardFlushBeforeDBWrite != nil {
		leaderboardFlushBeforeDBWrite()
	}
	if err := upsertLeaderboardLiveRows(ctx, rows); err != nil {
		leaderboardHourlyCacheLock.Lock()
		for key, row := range snapshot {
			if current, ok := leaderboardHourlyCache[key]; ok {
				current.StatsMetrics.Add(row.StatsMetrics)
				if row.LastRequestAt > current.LastRequestAt {
					current.LastRequestAt = row.LastRequestAt
				}
				if row.DimensionName != "" {
					current.DimensionName = row.DimensionName
				}
			} else {
				copyRow := *row
				leaderboardHourlyCache[key] = &copyRow
			}
		}
		leaderboardHourlyCacheLock.Unlock()
		return err
	}
	return nil
}

func upsertLeaderboardLiveRows(ctx context.Context, rows []model.StatsLeaderboardHourly) error {
	if len(rows) == 0 {
		return nil
	}
	dbConn := db.GetDB().WithContext(ctx)
	return dbConn.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "hour"}, {Name: "dimension_type"}, {Name: "dimension_key"}, {Name: "source"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"dimension_name":  gorm.Expr(leaderboardExcludedValue(dbConn, "dimension_name")),
			"date":            gorm.Expr(leaderboardExcludedValue(dbConn, "date")),
			"input_token":     leaderboardAddExpr(dbConn, "input_token"),
			"output_token":    leaderboardAddExpr(dbConn, "output_token"),
			"input_cost":      leaderboardAddExpr(dbConn, "input_cost"),
			"output_cost":     leaderboardAddExpr(dbConn, "output_cost"),
			"wait_time":       leaderboardAddExpr(dbConn, "wait_time"),
			"request_success": leaderboardAddExpr(dbConn, "request_success"),
			"request_failed":  leaderboardAddExpr(dbConn, "request_failed"),
			"last_request_at": gorm.Expr(leaderboardMaxExpr(dbConn, "last_request_at")),
		}),
	}).CreateInBatches(&rows, 200).Error
}

func leaderboardAddExpr(dbConn *gorm.DB, column string) interface{} {
	return gorm.Expr("stats_leaderboard_hourlies." + column + " + " + leaderboardExcludedValue(dbConn, column))
}

func leaderboardExcludedValue(dbConn *gorm.DB, column string) string {
	if dbConn.Dialector != nil && dbConn.Dialector.Name() == "mysql" {
		return "VALUES(" + column + ")"
	}
	return "EXCLUDED." + column
}

func leaderboardMaxExpr(dbConn *gorm.DB, column string) string {
	if dbConn.Dialector != nil && dbConn.Dialector.Name() == "sqlite" {
		return "MAX(stats_leaderboard_hourlies." + column + ", " + leaderboardExcludedValue(dbConn, column) + ")"
	}
	return "GREATEST(stats_leaderboard_hourlies." + column + ", " + leaderboardExcludedValue(dbConn, column) + ")"
}

func normalizeLeaderboardText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
