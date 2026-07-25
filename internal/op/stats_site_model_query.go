package op

import (
	"context"
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

const siteChannelModelHistoryWindow = 90 * 24 * time.Hour

type siteModelCompositeKey struct {
	SiteAccountID int
	Hour          int
	GroupKey      string
	ModelName     string
}

type siteModelGroupedSeries struct {
	Hours []model.StatsSiteModelHourly
}

// SiteChannelModelHourlyForAccount 读取指定 site account 下最近一段时间的 (group, model) 小时聚合，
// 合并未刷盘的内存桶后，按自适应桶宽生成 SiteModelHistorySummary。
// key 与 site_channel.go 保持一致：baseGroupKey + "\x00" + modelName。
func SiteChannelModelHourlyForAccount(
	ctx context.Context,
	siteAccountID int,
) (map[string]*model.SiteModelHistorySummary, error) {
	result, err := SiteChannelModelHourlyForAccounts(ctx, []int{siteAccountID})
	if err != nil {
		return nil, err
	}
	if result[siteAccountID] == nil {
		return map[string]*model.SiteModelHistorySummary{}, nil
	}
	return result[siteAccountID], nil
}

func SiteChannelModelHourlyForAccounts(
	ctx context.Context,
	siteAccountIDs []int,
) (map[int]map[string]*model.SiteModelHistorySummary, error) {
	ids, accountSet := normalizeSiteModelAccountIDs(siteAccountIDs)
	if len(ids) == 0 {
		return map[int]map[string]*model.SiteModelHistorySummary{}, nil
	}

	minHour := int(time.Now().Add(-siteChannelModelHistoryWindow).Unix() / 3600)
	rows, pending, err := loadSiteModelHourlySnapshots(
		normalizeBackfillContext(ctx),
		ids,
		accountSet,
		minHour,
	)
	if err != nil {
		return nil, err
	}
	merged := mergeSiteModelHourlyRows(rows, pending)
	grouped := groupSiteModelHourlyRows(merged)
	return buildSiteModelAccountResults(ids, grouped), nil
}

func normalizeSiteModelAccountIDs(siteAccountIDs []int) ([]int, map[int]struct{}) {
	accountSet := make(map[int]struct{}, len(siteAccountIDs))
	ids := make([]int, 0, len(siteAccountIDs))
	for _, id := range siteAccountIDs {
		if id <= 0 {
			continue
		}
		if _, exists := accountSet[id]; exists {
			continue
		}
		accountSet[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, accountSet
}

func loadSiteModelHourlySnapshots(
	ctx context.Context,
	ids []int,
	accountSet map[int]struct{},
	minHour int,
) ([]model.StatsSiteModelHourly, []model.StatsSiteModelHourly, error) {
	siteModelHourlyFlushLock.RLock()
	defer siteModelHourlyFlushLock.RUnlock()

	var rows []model.StatsSiteModelHourly
	if err := db.GetDB().WithContext(ctx).
		Where("site_account_id IN ? AND hour >= ?", ids, minHour).
		Order("site_account_id ASC").
		Order("hour ASC").
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}

	siteModelHourlyCacheLock.Lock()
	defer siteModelHourlyCacheLock.Unlock()
	pending := make([]model.StatsSiteModelHourly, 0, len(siteModelHourlyCache))
	for key, entry := range siteModelHourlyCache {
		if _, ok := accountSet[key.SiteAccountID]; ok && key.Hour >= minHour {
			pending = append(pending, *entry)
		}
	}
	return rows, pending, nil
}

func mergeSiteModelHourlyRows(
	persisted []model.StatsSiteModelHourly,
	pending []model.StatsSiteModelHourly,
) map[siteModelCompositeKey]*model.StatsSiteModelHourly {
	merged := make(map[siteModelCompositeKey]*model.StatsSiteModelHourly, len(persisted)+len(pending))
	for _, row := range persisted {
		mergeSiteModelHourlyRow(merged, row)
	}
	for _, row := range pending {
		mergeSiteModelHourlyRow(merged, row)
	}
	return merged
}

func mergeSiteModelHourlyRow(
	merged map[siteModelCompositeKey]*model.StatsSiteModelHourly,
	row model.StatsSiteModelHourly,
) {
	key := siteModelCompositeKey{
		SiteAccountID: row.SiteAccountID,
		Hour:          row.Hour,
		GroupKey:      row.GroupKey,
		ModelName:     row.ModelName,
	}
	if existing := merged[key]; existing != nil {
		existing.StatsMetrics.Add(row.StatsMetrics)
		if row.LastRequestAt > existing.LastRequestAt {
			existing.LastRequestAt = row.LastRequestAt
		}
		return
	}
	copyRow := row
	merged[key] = &copyRow
}

func groupSiteModelHourlyRows(
	merged map[siteModelCompositeKey]*model.StatsSiteModelHourly,
) map[int]map[string]*siteModelGroupedSeries {
	groupedByAccount := make(map[int]map[string]*siteModelGroupedSeries)
	for _, entry := range merged {
		key := entry.GroupKey + "\x00" + entry.ModelName
		grouped := groupedByAccount[entry.SiteAccountID]
		if grouped == nil {
			grouped = make(map[string]*siteModelGroupedSeries)
			groupedByAccount[entry.SiteAccountID] = grouped
		}
		series := grouped[key]
		if series == nil {
			series = &siteModelGroupedSeries{}
			grouped[key] = series
		}
		series.Hours = append(series.Hours, *entry)
	}
	return groupedByAccount
}

func buildSiteModelAccountResults(
	ids []int,
	groupedByAccount map[int]map[string]*siteModelGroupedSeries,
) map[int]map[string]*model.SiteModelHistorySummary {
	result := make(map[int]map[string]*model.SiteModelHistorySummary, len(ids))
	for _, id := range ids {
		result[id] = make(map[string]*model.SiteModelHistorySummary)
	}
	for accountID, grouped := range groupedByAccount {
		accountResult := result[accountID]
		for key, series := range grouped {
			sort.Slice(series.Hours, func(i, j int) bool {
				return series.Hours[i].Hour < series.Hours[j].Hour
			})
			accountResult[key] = buildSiteModelSummary(series.Hours)
		}
	}
	return result
}

// buildSiteModelSummary 把按时间排序的小时记录聚合为 SiteModelHistorySummary，
// 自适应选择桶宽。
func buildSiteModelSummary(hours []model.StatsSiteModelHourly) *model.SiteModelHistorySummary {
	summary := &model.SiteModelHistorySummary{}
	if len(hours) == 0 {
		return summary
	}

	success, failed, maxLast := siteModelSummaryTotals(hours)
	summary.SuccessCount = success
	summary.FailureCount = failed
	latestHour := hours[len(hours)-1].Hour
	if maxLast > 0 {
		summary.LastRequestAt = &maxLast
	} else {
		latestSec := int64(latestHour+1)*3600 - 1
		summary.LastRequestAt = &latestSec
	}

	spanSeconds := int64((latestHour - hours[0].Hour + 1) * 3600)
	summary.BucketSpan = chooseBucketSpan(spanSeconds)
	summary.Buckets = buildSiteModelHistoryBuckets(hours, summary.BucketSpan)
	return summary
}

func siteModelSummaryTotals(hours []model.StatsSiteModelHourly) (int, int, int64) {
	var success, failed int
	var maxLast int64
	for i := range hours {
		success += int(hours[i].RequestSuccess)
		failed += int(hours[i].RequestFailed)
		if hours[i].LastRequestAt > maxLast {
			maxLast = hours[i].LastRequestAt
		}
	}
	return success, failed, maxLast
}

func buildSiteModelHistoryBuckets(
	hours []model.StatsSiteModelHourly,
	bucketSpan int,
) []model.SiteModelHistoryBucket {
	bucketMap := make(map[int64]*model.SiteModelHistoryBucket)
	for _, hourly := range hours {
		hourStart := int64(hourly.Hour) * 3600
		bucketStart := hourStart - hourStart%int64(bucketSpan)
		bucket := bucketMap[bucketStart]
		if bucket == nil {
			bucket = &model.SiteModelHistoryBucket{Time: bucketStart}
			bucketMap[bucketStart] = bucket
		}
		bucket.Success += int(hourly.RequestSuccess)
		bucket.Failure += int(hourly.RequestFailed)
	}

	buckets := make([]model.SiteModelHistoryBucket, 0, len(bucketMap))
	for _, bucket := range bucketMap {
		buckets = append(buckets, *bucket)
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Time < buckets[j].Time
	})
	return buckets
}

func chooseBucketSpan(spanSeconds int64) int {
	const (
		hour = int64(3600)
		day  = 24 * hour
		week = 7 * day
	)
	switch {
	case spanSeconds <= 24*hour:
		return int(hour)
	case spanSeconds <= 7*day:
		return int(6 * hour)
	case spanSeconds <= 30*day:
		return int(day)
	default:
		return int(week)
	}
}
