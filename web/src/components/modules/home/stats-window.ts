import type { StatsDailyFormatted } from '@/api/endpoints/stats';
import { formatMoney } from '@/lib/utils';

export type ChartPoint = { date: string; total_cost: number };

export const STATS_TIME_ZONE = 'Asia/Shanghai';

/** Return the current calendar date in the statistics time zone. */
export function shanghaiDateKey(date = new Date()): string {
    const parts = new Intl.DateTimeFormat('en-CA', {
        timeZone: STATS_TIME_ZONE,
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
    }).formatToParts(date);
    const part = (type: string) => parts.find((item) => item.type === type)?.value ?? '';
    return `${part('year')}${part('month')}${part('day')}`;
}

/** Shift a compact YYYYMMDD key by a number of calendar days. */
export function shiftDateKey(key: string, days: number): string {
    const date = new Date(Date.UTC(
        Number(key.slice(0, 4)),
        Number(key.slice(4, 6)) - 1,
        Number(key.slice(6, 8)),
    ));
    date.setUTCDate(date.getUTCDate() + days);
    return date.toISOString().slice(0, 10).replaceAll('-', '');
}

/** Format a compact date key for the chart axis. */
export function formatDateLabel(key: string): string {
    if (key.length !== 8) return key;
    return `${key.slice(4, 6)}/${key.slice(6, 8)}`;
}

/** Format chart currency with the currency sign before the value. */
export function formatMoneyLabel(value: number): string {
    const formatted = formatMoney(value).formatted;
    const scale = formatted.unit.endsWith('$') ? formatted.unit.slice(0, -1) : formatted.unit;
    return `$${formatted.value}${scale}`;
}

/** Build a natural-day window and zero-fill dates with no requests. */
export function dailyWindowPoints(sortedDaily: StatsDailyFormatted[], days: number): {
    points: ChartPoint[];
    values: StatsDailyFormatted[];
} {
    const end = shanghaiDateKey();
    const start = shiftDateKey(end, -(days - 1));
    const byDate = new Map(sortedDaily.map((stat) => [stat.date, stat]));
    const values: StatsDailyFormatted[] = [];
    const points: ChartPoint[] = [];
    for (let offset = 0; offset < days; offset += 1) {
        const date = shiftDateKey(start, offset);
        const stat = byDate.get(date);
        if (stat) values.push(stat);
        points.push({ date: formatDateLabel(date), total_cost: stat?.total_cost.raw ?? 0 });
    }
    return { points, values };
}
