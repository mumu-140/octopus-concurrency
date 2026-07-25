import type {
    StatsDailyFormatted,
    StatsHourlyFormatted,
    StatsTotalFormatted,
} from '@/api/endpoints/stats';
import type { ChartPeriod } from '@/components/modules/home/store';
import {
    dailyWindowPoints,
    formatDateLabel,
    type ChartPoint,
} from '@/components/modules/home/stats-window';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';

export type Formatted = { value: string; unit: string };

export type MetricsRow = {
    requests: Formatted;
    tokens: Formatted;
    waitTime: Formatted;
};

export type HeroValue = {
    value: string | undefined;
    unit: string;
};

export type StatsChartSummary = {
    hero: HeroValue;
    metrics: MetricsRow;
    chartData: ChartPoint[];
};

type SummaryMetricSource = Pick<
    StatsDailyFormatted,
    'total_cost' | 'request_count' | 'total_token' | 'wait_time'
>;

function emptyMetrics(): MetricsRow {
    return {
        requests: formatCount(0).formatted,
        tokens: formatCount(0).formatted,
        waitTime: formatTime(0).formatted,
    };
}

function emptySummary(chartData: ChartPoint[] = []): StatsChartSummary {
    return {
        hero: { value: undefined, unit: '' },
        metrics: emptyMetrics(),
        chartData,
    };
}

function summaryFromRows(
    rows: SummaryMetricSource[],
    chartData: ChartPoint[],
): StatsChartSummary {
    if (rows.length === 0) {
        return emptySummary(chartData);
    }
    const cost = rows.reduce((sum, row) => sum + row.total_cost.raw, 0);
    const requests = rows.reduce((sum, row) => sum + row.request_count.raw, 0);
    const tokens = rows.reduce((sum, row) => sum + row.total_token.raw, 0);
    const wait = rows.reduce((sum, row) => sum + row.wait_time.raw, 0);
    const costFormatted = formatMoney(cost).formatted;
    return {
        hero: { value: costFormatted.value, unit: costFormatted.unit },
        metrics: {
            requests: formatCount(requests).formatted,
            tokens: formatCount(tokens).formatted,
            waitTime: formatTime(wait).formatted,
        },
        chartData,
    };
}

function allTimeSummary(
    statsTotal: StatsTotalFormatted | undefined,
    sortedDaily: StatsDailyFormatted[],
): StatsChartSummary {
    const chartData = sortedDaily.map((stat) => ({
        date: formatDateLabel(stat.date),
        total_cost: stat.total_cost.raw,
    }));
    if (!statsTotal) {
        return summaryFromRows(sortedDaily, chartData);
    }
    return {
        hero: {
            value: statsTotal.total_cost.formatted.value,
            unit: statsTotal.total_cost.formatted.unit,
        },
        metrics: {
            requests: statsTotal.request_count.formatted,
            tokens: statsTotal.total_token.formatted,
            waitTime: statsTotal.wait_time.formatted,
        },
        chartData,
    };
}

function todaySummary(statsHourly: StatsHourlyFormatted[] | undefined): StatsChartSummary {
    if (!statsHourly) {
        return emptySummary();
    }
    const chartData = statsHourly.map((stat) => ({
        date: `${stat.hour}:00`,
        total_cost: stat.total_cost.raw,
    }));
    return summaryFromRows(statsHourly, chartData);
}

function naturalDaySummary(
    sortedDaily: StatsDailyFormatted[],
    days: number,
): StatsChartSummary {
    const { points, values } = dailyWindowPoints(sortedDaily, days);
    return summaryFromRows(values, points);
}

export function buildStatsChartSummary(
    period: ChartPeriod,
    statsTotal: StatsTotalFormatted | undefined,
    statsHourly: StatsHourlyFormatted[] | undefined,
    sortedDaily: StatsDailyFormatted[],
): StatsChartSummary {
    if (period === 'all') {
        return allTimeSummary(statsTotal, sortedDaily);
    }
    if (period === '1') {
        return todaySummary(statsHourly);
    }
    return naturalDaySummary(sortedDaily, Number(period));
}
