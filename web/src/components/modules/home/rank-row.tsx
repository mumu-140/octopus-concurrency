import type { LeaderboardEntry } from '@/api/endpoints/stats';
import type { RankSortMode } from '@/components/modules/home/store';

export type RankTranslate = (
    key: string,
    values?: Record<string, string | number>,
) => string;

export function LeaderboardRow({
    row,
    rank,
    mode,
    t,
}: {
    row: LeaderboardEntry;
    rank: number;
    mode: RankSortMode;
    t: RankTranslate;
}) {
    const total = row.formatted.request_success.raw + row.formatted.request_failed.raw;
    const successRate = total > 0 ? (row.formatted.request_success.raw / total) * 100 : 0;
    return (
        <div className="flex items-center gap-3 rounded-xl px-2 py-2 hover:bg-accent/5 transition-colors">
            <RankBadge rank={rank} label={t('rankLabel', { rank })} />
            <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{row.name}</p>
                {mode === 'count' && (
                    <p className="mt-0.5 text-xs text-muted-foreground">
                        {t('successRate')}: {successRate.toFixed(1)}%
                    </p>
                )}
            </div>
            <div className="shrink-0 text-right tabular-nums">
                <LeaderboardValue row={row} mode={mode} />
            </div>
        </div>
    );
}

function RankBadge({ rank, label }: { rank: number; label: string }) {
    const appearance = rank <= 3
        ? 'bg-primary/10 text-primary'
        : 'bg-muted text-muted-foreground';
    return (
        <div
            className={`flex size-7 shrink-0 items-center justify-center rounded-lg text-sm font-semibold tabular-nums ${appearance}`}
            aria-label={label}
        >
            {rank}
        </div>
    );
}

function LeaderboardValue({
    row,
    mode,
}: {
    row: LeaderboardEntry;
    mode: RankSortMode;
}) {
    if (mode === 'count') {
        return <CountValue row={row} />;
    }
    if (mode === 'tokens') {
        return (
            <MetricValue
                value={row.formatted.total_token.formatted.value}
                unit={row.formatted.total_token.formatted.unit}
            />
        );
    }
    if (mode === 'failed') {
        return (
            <MetricValue
                value={row.formatted.request_failed.formatted.value}
                unit={row.formatted.request_failed.formatted.unit}
                destructive
            />
        );
    }
    return (
        <MoneyValue
            value={row.formatted.total_cost.formatted.value}
            unit={row.formatted.total_cost.formatted.unit}
        />
    );
}

function CountValue({ row }: { row: LeaderboardEntry }) {
    const success = row.formatted.request_success.formatted;
    const failed = row.formatted.request_failed.formatted;
    return (
        <span className="text-sm font-medium">
            <MetricPart value={success.value} unit={success.unit} className="text-primary" />
            <span className="mx-1 text-muted-foreground/50">/</span>
            <MetricPart value={failed.value} unit={failed.unit} className="text-destructive" />
        </span>
    );
}

function MetricPart({
    value,
    unit,
    className,
}: {
    value: string;
    unit: string;
    className: string;
}) {
    return (
        <span className={className}>
            {value}
            {unit && <span className="text-xs font-normal">{unit}</span>}
        </span>
    );
}

function MoneyValue({ value, unit }: { value: string; unit: string }) {
    const scale = unit.endsWith('$') ? unit.slice(0, -1) : unit;
    return (
        <span className="text-sm font-semibold">
            <span className="mr-0.5 text-xs font-normal text-muted-foreground">$</span>
            {value}
            {scale && <span className="ml-0.5 text-xs font-normal text-muted-foreground">{scale}</span>}
        </span>
    );
}

function MetricValue({
    value,
    unit,
    destructive = false,
}: {
    value: string;
    unit: string;
    destructive?: boolean;
}) {
    return (
        <span className={`text-sm font-semibold ${destructive ? 'text-destructive' : ''}`}>
            {value}
            {unit && <span className="ml-0.5 text-xs font-normal text-muted-foreground">{unit}</span>}
        </span>
    );
}
