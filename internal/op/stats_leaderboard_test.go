package op

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func resetStatsLeaderboardTestState(t *testing.T, ctx context.Context) {
	t.Helper()

	leaderboardHourlyCacheLock.Lock()
	leaderboardHourlyCache = make(map[leaderboardHourlyKey]*model.StatsLeaderboardHourly)
	leaderboardHourlyCacheLock.Unlock()

	if err := statsRefreshCache(ctx); err != nil {
		t.Fatalf("statsRefreshCache failed: %v", err)
	}

	originalNow := statsLeaderboardNow
	t.Cleanup(func() {
		statsLeaderboardNow = originalNow
		leaderboardHourlyCacheLock.Lock()
		leaderboardHourlyCache = make(map[leaderboardHourlyKey]*model.StatsLeaderboardHourly)
		leaderboardHourlyCacheLock.Unlock()
	})
}

func leaderboardRowByKey(t *testing.T, result StatsLeaderboardResult, key string) StatsLeaderboardRow {
	t.Helper()
	for _, row := range result.Rows {
		if row.Key == key {
			return row
		}
	}
	t.Fatalf("leaderboard row %q not found in %+v", key, result.Rows)
	return StatsLeaderboardRow{}
}

func findLeaderboardRow(result StatsLeaderboardResult, key string) (StatsLeaderboardRow, bool) {
	for _, row := range result.Rows {
		if row.Key == key {
			return row, true
		}
	}
	return StatsLeaderboardRow{}, false
}

func leaderboardRequestCount(result StatsLeaderboardResult) int64 {
	var total int64
	for _, row := range result.Rows {
		total += row.RequestSuccess + row.RequestFailed
	}
	return total
}

func TestRecordStatsEventWritesAllFinalRequestDimensions(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	resetStatsLeaderboardTestState(t, ctx)

	now := time.Date(2026, time.July, 25, 9, 30, 0, 0, time.Local)
	statsLeaderboardNow = func() time.Time { return now }

	metrics := model.StatsMetrics{
		InputToken:     120,
		OutputToken:    30,
		InputCost:      0.12,
		OutputCost:     0.03,
		WaitTime:       450,
		RequestSuccess: 1,
	}
	RecordStatsEvent(ctx, StatsLeaderboardEvent{
		APIKeyID:     7,
		RequestModel: "group-gpt",
		ActualModel:  "gpt-5.6-terra",
		ChannelID:    42,
		ChannelName:  "primary-channel",
		Metrics:      metrics,
	})

	checks := []struct {
		dimension string
		key       string
		name      string
	}{
		{model.StatsLeaderboardDimensionModel, "gpt-5.6-terra", "gpt-5.6-terra"},
		{model.StatsLeaderboardDimensionChannel, "42", "primary-channel"},
		{model.StatsLeaderboardDimensionGroup, "group-gpt", "group-gpt"},
	}
	for _, check := range checks {
		result, err := StatsLeaderboardQuery(ctx, check.dimension, StatsLeaderboardWindowToday)
		if err != nil {
			t.Fatalf("query %s failed: %v", check.dimension, err)
		}
		row := leaderboardRowByKey(t, result, check.key)
		if row.Name != check.name || row.StatsMetrics != metrics {
			t.Fatalf("unexpected %s row: %+v", check.dimension, row)
		}
	}

	total := StatsTotalGet()
	if total.StatsMetrics != metrics {
		t.Fatalf("global total does not reconcile with final request: got %+v want %+v", total.StatsMetrics, metrics)
	}
	apiKey := StatsAPIKeyGet(7)
	if apiKey.StatsMetrics != metrics {
		t.Fatalf("API key stats not updated by unified event: got %+v want %+v", apiKey.StatsMetrics, metrics)
	}
}

func TestRecordStatsEventPersistsDateRolloverAfterClientCancellation(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	resetStatsLeaderboardTestState(t, ctx)

	previousDate := time.Now().Add(-24 * time.Hour).Format("20060102")
	statsDailyCacheLock.Lock()
	statsDailyCache = model.StatsDaily{
		Date:         previousDate,
		StatsMetrics: model.StatsMetrics{RequestSuccess: 3},
	}
	statsDailyCacheLock.Unlock()

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	RecordStatsEvent(canceled, StatsLeaderboardEvent{
		RequestModel: "group-canceled",
		ActualModel:  "model-canceled",
		Metrics:      model.StatsMetrics{RequestFailed: 1},
	})

	var persisted model.StatsDaily
	if err := dbpkg.GetDB().WithContext(ctx).First(&persisted, "date = ?", previousDate).Error; err != nil {
		t.Fatalf("read persisted previous day failed: %v", err)
	}
	if persisted.RequestSuccess != 3 {
		t.Fatalf("previous day requests = %d, want 3", persisted.RequestSuccess)
	}
}

func TestStatsLeaderboardQueryUsesLatestDimensionName(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	resetStatsLeaderboardTestState(t, ctx)

	rows := []model.StatsLeaderboardHourly{
		// Insert the newest row first so an implementation that blindly
		// overwrites names based on query order is caught deterministically.
		{Hour: 2, DimensionType: model.StatsLeaderboardDimensionChannel, DimensionKey: "7", DimensionName: "new-name", Source: model.StatsLeaderboardSourceLive, LastRequestAt: 200, StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
		{Hour: 1, DimensionType: model.StatsLeaderboardDimensionChannel, DimensionKey: "7", DimensionName: "old-name", Source: model.StatsLeaderboardSourceBackfill, LastRequestAt: 100, StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create leaderboard rows failed: %v", err)
	}
	result, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionChannel, StatsLeaderboardWindowAll)
	if err != nil {
		t.Fatalf("query leaderboard failed: %v", err)
	}
	row := leaderboardRowByKey(t, result, "7")
	if row.Name != "new-name" {
		t.Fatalf("channel name = %q, want latest new-name", row.Name)
	}
}

func TestStatsLeaderboardFlushDoesNotDoubleCountPendingCache(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	resetStatsLeaderboardTestState(t, ctx)
	statsLeaderboardNow = func() time.Time { return time.Date(2026, time.July, 25, 10, 0, 0, 0, time.Local) }

	event := StatsLeaderboardEvent{
		RequestModel: "group-a",
		ActualModel:  "model-a",
		ChannelID:    9,
		ChannelName:  "channel-a",
		Metrics:      model.StatsMetrics{RequestSuccess: 1, InputToken: 5},
	}
	RecordStatsEvent(ctx, event)
	before, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionModel, StatsLeaderboardWindowAll)
	if err != nil {
		t.Fatalf("query before flush failed: %v", err)
	}
	if got := leaderboardRequestCount(before); got != 1 {
		t.Fatalf("request count before flush = %d, want 1", got)
	}

	if err := StatsLeaderboardHourlySaveDB(ctx); err != nil {
		t.Fatalf("flush failed: %v", err)
	}
	after, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionModel, StatsLeaderboardWindowAll)
	if err != nil {
		t.Fatalf("query after flush failed: %v", err)
	}
	if got := leaderboardRequestCount(after); got != 1 {
		t.Fatalf("request count after flush = %d, want 1", got)
	}

	RecordStatsEvent(ctx, event)
	mixed, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionModel, StatsLeaderboardWindowAll)
	if err != nil {
		t.Fatalf("query persisted + pending failed: %v", err)
	}
	if got := leaderboardRequestCount(mixed); got != 2 {
		t.Fatalf("request count with persisted + pending = %d, want 2", got)
	}
}

func TestStatsLeaderboardQueryWaitsForFlushCommit(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	resetStatsLeaderboardTestState(t, ctx)
	statsLeaderboardNow = func() time.Time { return time.Date(2026, time.July, 25, 10, 0, 0, 0, time.Local) }

	RecordStatsEvent(ctx, StatsLeaderboardEvent{
		RequestModel: "group-a",
		ActualModel:  "model-a",
		Metrics:      model.StatsMetrics{RequestSuccess: 1},
	})

	started := make(chan struct{})
	release := make(chan struct{})
	leaderboardFlushBeforeDBWrite = func() {
		close(started)
		<-release
	}
	t.Cleanup(func() { leaderboardFlushBeforeDBWrite = nil })

	flushDone := make(chan error, 1)
	go func() { flushDone <- StatsLeaderboardHourlySaveDB(ctx) }()
	<-started

	queryDone := make(chan StatsLeaderboardResult, 1)
	queryErr := make(chan error, 1)
	go func() {
		result, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionModel, StatsLeaderboardWindowAll)
		if err != nil {
			queryErr <- err
			return
		}
		queryDone <- result
	}()

	select {
	case <-queryDone:
		t.Fatalf("query returned while flush transaction was still in flight")
	case <-queryErr:
		t.Fatalf("query failed while flush transaction was still in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-flushDone; err != nil {
		t.Fatalf("flush failed: %v", err)
	}
	select {
	case result := <-queryDone:
		if got := leaderboardRequestCount(result); got != 1 {
			t.Fatalf("query after flush returned %d requests, want 1", got)
		}
	case err := <-queryErr:
		t.Fatalf("query after flush failed: %v", err)
	case <-time.After(time.Second):
		t.Fatalf("query did not finish after flush commit")
	}
}

func TestStatsLeaderboardBackfillIsIdempotentAndCombinesWithLive(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	resetStatsLeaderboardTestState(t, ctx)

	settingCache.Set(model.SettingKeyRelayLogKeepEnabled, "true")
	settingCache.Set(model.SettingKeyRelayLogKeepPeriod, "0")
	t.Cleanup(func() {
		settingCache.Del(model.SettingKeyRelayLogKeepEnabled)
		settingCache.Del(model.SettingKeyRelayLogKeepPeriod)
	})
	if err := SettingSetInt(model.SettingKeyRelayLogKeepPeriod, 0); err != nil {
		t.Fatalf("disable retention for deterministic 30-day fixture: %v", err)
	}
	cutoff := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.Local).Unix()
	heavy := strings.Repeat("x", 64*1024)
	logs := []model.RelayLog{
		{
			ID:               1,
			Time:             cutoff - 7200,
			RequestModelName: "group-a",
			ActualModelName:  "model-a",
			ChannelId:        11,
			ChannelName:      "channel-a",
			InputTokens:      10,
			OutputTokens:     2,
			Cost:             0.5,
			UseTime:          100,
			Success:          true,
			RequestContent:   heavy,
			ResponseContent:  heavy,
		},
		{
			ID:               2,
			Time:             cutoff - 3600,
			RequestModelName: "group-b",
			ActualModelName:  "",
			ChannelId:        0,
			InputTokens:      20,
			OutputTokens:     3,
			Cost:             0.75,
			UseTime:          200,
			Success:          false,
			Error:            "",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 12, ChannelName: "channel-b", ModelName: "model-b", Status: model.AttemptFailed},
			},
			RequestContent:  heavy,
			ResponseContent: heavy,
		},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&logs).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	if err := statsLeaderboardBackfill(ctx, cutoff); err != nil {
		t.Fatalf("first backfill failed: %v", err)
	}
	var coverage model.StatsLeaderboardCoverage
	if err := dbpkg.GetDB().WithContext(ctx).First(&coverage, 1).Error; err != nil {
		t.Fatalf("read coverage failed: %v", err)
	}
	wantCoverageStart := cutoff - int64(statsLeaderboardBackfillWindow/time.Second)
	if coverage.EarliestEventAt != wantCoverageStart {
		t.Fatalf("coverage start = %d, want scanned start %d", coverage.EarliestEventAt, wantCoverageStart)
	}
	for _, dimension := range []string{
		model.StatsLeaderboardDimensionModel,
		model.StatsLeaderboardDimensionChannel,
		model.StatsLeaderboardDimensionGroup,
	} {
		result, err := StatsLeaderboardQuery(ctx, dimension, StatsLeaderboardWindowAll)
		if err != nil {
			t.Fatalf("query %s after backfill failed: %v", dimension, err)
		}
		if got := leaderboardRequestCount(result); got != 2 {
			t.Fatalf("%s backfill request count = %d, want 2", dimension, got)
		}
		if dimension == model.StatsLeaderboardDimensionModel {
			if _, ok := findLeaderboardRow(result, "model-b"); !ok {
				t.Fatalf("failed relay should be attributed to final attempted model")
			}
			failed := leaderboardRowByKey(t, result, "model-b")
			if failed.RequestFailed != 1 || failed.RequestSuccess != 0 {
				t.Fatalf("empty-error failure was not preserved: %+v", failed)
			}
		}
	}

	if err := dbpkg.GetDB().Model(&model.StatsLeaderboardCoverage{}).
		Where("id = ?", 1).
		Updates(map[string]any{"status": model.StatsLeaderboardCoverageFailed, "completed_at": 0}).Error; err != nil {
		t.Fatalf("reset coverage for retry failed: %v", err)
	}
	if err := statsLeaderboardBackfill(ctx, cutoff); err != nil {
		t.Fatalf("retry backfill failed: %v", err)
	}
	retried, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionGroup, StatsLeaderboardWindowAll)
	if err != nil {
		t.Fatalf("query after retry failed: %v", err)
	}
	if got := leaderboardRequestCount(retried); got != 2 {
		t.Fatalf("idempotent retry request count = %d, want 2", got)
	}

	statsLeaderboardNow = func() time.Time { return time.Unix(cutoff+60, 0) }
	RecordStatsEvent(ctx, StatsLeaderboardEvent{
		RequestModel: "group-live",
		ActualModel:  "model-live",
		ChannelID:    13,
		ChannelName:  "channel-live",
		Metrics:      model.StatsMetrics{RequestSuccess: 1, InputToken: 4},
	})
	combined, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionGroup, StatsLeaderboardWindowAll)
	if err != nil {
		t.Fatalf("query backfill + live failed: %v", err)
	}
	if got := leaderboardRequestCount(combined); got != 3 {
		t.Fatalf("backfill + live request count = %d, want 3", got)
	}
}

func TestStatsLeaderboardBackfillProjectionSkipsHeavyContent(t *testing.T) {
	rowType := reflect.TypeOf(leaderboardBackfillLogRow{})
	for _, name := range []string{"RequestContent", "ResponseContent"} {
		if _, ok := rowType.FieldByName(name); ok {
			t.Fatalf("leaderboardBackfillLogRow must not contain %q", name)
		}
	}
}

func TestStatsLeaderboardWindowsAndCoverage(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	resetStatsLeaderboardTestState(t, ctx)

	now := time.Date(2026, time.July, 25, 15, 0, 0, 0, time.Local)
	statsLeaderboardNow = func() time.Time { return now }
	rows := []model.StatsLeaderboardHourly{
		{Hour: int(now.Add(-time.Hour).Unix() / 3600), DimensionType: model.StatsLeaderboardDimensionModel, DimensionKey: "model-a", DimensionName: "model-a", Source: model.StatsLeaderboardSourceBackfill, Date: now.Format("20060102"), StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
		// This falls inside a rolling seven-day window, but outside the
		// current-day-plus-six-calendar-days chart window.
		{Hour: int(now.Add(-6*24*time.Hour-19*time.Hour).Unix() / 3600), DimensionType: model.StatsLeaderboardDimensionModel, DimensionKey: "model-a", DimensionName: "model-a", Source: model.StatsLeaderboardSourceBackfill, Date: now.Add(-6*24*time.Hour - 19*time.Hour).Format("20060102"), StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
		{Hour: int(now.Add(-8*24*time.Hour).Unix() / 3600), DimensionType: model.StatsLeaderboardDimensionModel, DimensionKey: "model-a", DimensionName: "model-a", Source: model.StatsLeaderboardSourceBackfill, Date: now.Add(-8 * 24 * time.Hour).Format("20060102"), StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
		{Hour: int(now.Add(-31*24*time.Hour).Unix() / 3600), DimensionType: model.StatsLeaderboardDimensionModel, DimensionKey: "model-a", DimensionName: "model-a", Source: model.StatsLeaderboardSourceBackfill, Date: now.Add(-31 * 24 * time.Hour).Format("20060102"), StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create leaderboard rows failed: %v", err)
	}
	coverage := model.StatsLeaderboardCoverage{
		ID:              1,
		Version:         statsLeaderboardBackfillVersion,
		Status:          model.StatsLeaderboardCoverageDone,
		EarliestEventAt: now.Add(-8 * 24 * time.Hour).Unix(),
		BackfillCutoff:  now.Add(-time.Minute).Unix(),
		CompletedAt:     now.Unix(),
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&coverage).Error; err != nil {
		t.Fatalf("create coverage failed: %v", err)
	}

	cases := []struct {
		window   StatsLeaderboardWindow
		requests int64
		complete bool
	}{
		{StatsLeaderboardWindow7Days, 1, true},
		{StatsLeaderboardWindow30Days, 3, false},
		{StatsLeaderboardWindowAll, 4, false},
	}
	for _, testCase := range cases {
		result, err := StatsLeaderboardQuery(ctx, model.StatsLeaderboardDimensionModel, testCase.window)
		if err != nil {
			t.Fatalf("query window %s failed: %v", testCase.window, err)
		}
		if got := leaderboardRequestCount(result); got != testCase.requests {
			t.Fatalf("window %s request count = %d, want %d", testCase.window, got, testCase.requests)
		}
		if result.Coverage.Complete != testCase.complete {
			t.Fatalf("window %s complete = %v, want %v", testCase.window, result.Coverage.Complete, testCase.complete)
		}
	}
}

func TestStatsChannelUpdateIgnoresUnassignedChannel(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := statsRefreshCache(ctx); err != nil {
		t.Fatalf("statsRefreshCache failed: %v", err)
	}

	if err := StatsChannelUpdate(0, model.StatsMetrics{RequestFailed: 1}); err != nil {
		t.Fatalf("StatsChannelUpdate failed: %v", err)
	}
	if _, ok := statsChannelCache.Get(0); ok {
		t.Fatalf("unassigned channel must not create a foreign-key-invalid stats row")
	}
}

func TestStatsSiteModelSaveRestoresDetachedRowsAfterFailure(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	key := siteModelHourlyKey{Hour: 17, SiteAccountID: 9, GroupKey: "group-a", ModelName: "model-a"}
	row := &model.StatsSiteModelHourly{
		Hour:          key.Hour,
		SiteAccountID: key.SiteAccountID,
		GroupKey:      key.GroupKey,
		ModelName:     key.ModelName,
		Date:          "20260725",
		StatsMetrics:  model.StatsMetrics{RequestSuccess: 2},
	}
	siteModelHourlyCacheLock.Lock()
	siteModelHourlyCache = map[siteModelHourlyKey]*model.StatsSiteModelHourly{key: row}
	siteModelHourlyCacheLock.Unlock()
	t.Cleanup(func() {
		siteModelHourlyCacheLock.Lock()
		siteModelHourlyCache = make(map[siteModelHourlyKey]*model.StatsSiteModelHourly)
		siteModelHourlyCacheLock.Unlock()
	})

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := StatsSiteModelHourlySaveDB(canceled); err == nil {
		t.Fatalf("expected canceled flush to fail")
	}

	siteModelHourlyCacheLock.Lock()
	restored, ok := siteModelHourlyCache[key]
	siteModelHourlyCacheLock.Unlock()
	if !ok || restored.RequestSuccess != 2 {
		t.Fatalf("failed flush discarded detached site-model stats: %+v", restored)
	}
}
