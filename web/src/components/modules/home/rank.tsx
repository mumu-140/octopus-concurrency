'use client';

import {
    useLeaderboard,
    type LeaderboardCoverage,
    type LeaderboardEntry,
} from '@/api/endpoints/stats';
import { Tabs, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import {
    LeaderboardRows,
    RANK_PAGE_SIZE,
} from '@/components/modules/home/rank-list';
import type { RankTranslate } from '@/components/modules/home/rank-row';
import { STATS_TIME_ZONE } from '@/components/modules/home/stats-window';
import {
    useHomeViewStore,
    type ChartPeriod,
    type RankDimension,
    type RankSortMode,
} from '@/components/modules/home/store';
import { AlertCircle, RefreshCw } from 'lucide-react';
import { useLocale, useTranslations } from 'next-intl';
import { useMemo } from 'react';

function toLeaderboardWindow(period: ChartPeriod): 'today' | '7' | '30' | 'all' {
    return period === '1' ? 'today' : period;
}

function leaderboardMetric(row: LeaderboardEntry, mode: RankSortMode): number {
    switch (mode) {
        case 'count':
            return row.formatted.request_count.raw;
        case 'tokens':
            return row.formatted.total_token.raw;
        case 'failed':
            return row.formatted.request_failed.raw;
        default:
            return row.formatted.total_cost.raw;
    }
}

function sortLeaderboardRows(
    rows: LeaderboardEntry[] | undefined,
    mode: RankSortMode,
): LeaderboardEntry[] {
    return [...(rows ?? [])].sort((left, right) => {
        const difference = leaderboardMetric(right, mode) - leaderboardMetric(left, mode);
        return difference !== 0 ? difference : left.key.localeCompare(right.key);
    });
}

function coverageMessage(
    coverage: LeaderboardCoverage | undefined,
    locale: string,
    t: RankTranslate,
): string | null {
    if (!coverage) return null;
    if (coverage.status === 'failed') return t('coverageFailed');
    if (coverage.status !== 'completed') return t('coveragePending');
    if (coverage.complete) return null;
    if (!coverage.earliest_event_at) return t('coveragePartial');
    const date = new Date(coverage.earliest_event_at * 1000).toLocaleDateString(locale, {
        timeZone: STATS_TIME_ZONE,
    });
    return t('coverageFrom', { date });
}

export function Rank() {
    const t = useTranslations('home.rank');
    const locale = useLocale();
    const rankSortMode = useHomeViewStore((state) => state.rankSortMode);
    const setRankSortMode = useHomeViewStore((state) => state.setRankSortMode);
    const rankDimension = useHomeViewStore((state) => state.rankDimension);
    const setRankDimension = useHomeViewStore((state) => state.setRankDimension);
    const chartPeriod = useHomeViewStore((state) => state.chartPeriod);
    const window = toLeaderboardWindow(chartPeriod);
    const query = useLeaderboard(rankDimension, window);
    const sortedRows = useMemo(
        () => sortLeaderboardRows(query.data?.rows, rankSortMode),
        [query.data?.rows, rankSortMode],
    );
    const pageCount = Math.max(1, Math.ceil(sortedRows.length / RANK_PAGE_SIZE));
    const paginationKey = `${rankDimension}:${rankSortMode}:${window}:${pageCount}`;
    const coverageText = coverageMessage(query.data?.coverage, locale, t);

    return (
        <section
            className="rounded-3xl bg-card text-card-foreground border-card-border border p-4 custom-shadow"
            aria-busy={query.isLoading}
        >
            <RankHeader
                dimension={rankDimension}
                setDimension={setRankDimension}
                sortMode={rankSortMode}
                setSortMode={setRankSortMode}
                showRetry={query.isError}
                onRetry={() => void query.refetch()}
                t={t}
            />
            {query.isError && query.data !== undefined && (
                <RankNotice message={t('staleError')} destructive />
            )}
            {coverageText && <RankNotice message={coverageText} />}
            <LeaderboardRows
                key={paginationKey}
                rows={sortedRows}
                mode={rankSortMode}
                initialLoading={query.isLoading && query.data === undefined}
                showBlockingError={query.isError && query.data === undefined}
                t={t}
            />
        </section>
    );
}

function RankHeader({
    dimension,
    setDimension,
    sortMode,
    setSortMode,
    showRetry,
    onRetry,
    t,
}: {
    dimension: RankDimension;
    setDimension: (dimension: RankDimension) => void;
    sortMode: RankSortMode;
    setSortMode: (mode: RankSortMode) => void;
    showRetry: boolean;
    onRetry: () => void;
    t: RankTranslate;
}) {
    return (
        <header className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                    <h3 className="font-semibold text-base truncate">{t('title')}</h3>
                    <p className="text-xs text-muted-foreground mt-0.5">{t('description')}</p>
                </div>
                {showRetry && <RetryButton onRetry={onRetry} t={t} />}
            </div>
            <RankControls
                dimension={dimension}
                setDimension={setDimension}
                sortMode={sortMode}
                setSortMode={setSortMode}
                t={t}
            />
        </header>
    );
}

function RetryButton({ onRetry, t }: { onRetry: () => void; t: RankTranslate }) {
    return (
        <button
            type="button"
            onClick={onRetry}
            className="inline-flex min-h-11 min-w-11 shrink-0 items-center justify-center gap-1 rounded-lg px-2 text-xs text-muted-foreground hover:bg-accent/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={t('retry')}
            title={t('retry')}
        >
            <RefreshCw className="size-3.5" />
            <span className="hidden sm:inline">{t('retry')}</span>
        </button>
    );
}

function RankControls({
    dimension,
    setDimension,
    sortMode,
    setSortMode,
    t,
}: {
    dimension: RankDimension;
    setDimension: (dimension: RankDimension) => void;
    sortMode: RankSortMode;
    setSortMode: (mode: RankSortMode) => void;
    t: RankTranslate;
}) {
    return (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div className="max-w-full overflow-x-auto pb-0.5">
                <Tabs value={dimension} onValueChange={(value) => setDimension(value as RankDimension)}>
                    <TabsList aria-label={t('dimensionLabel')}>
                        <TabsTrigger value="model">{t('modelMode')}</TabsTrigger>
                        <TabsTrigger value="channel">{t('channelMode')}</TabsTrigger>
                        <TabsTrigger value="group">{t('groupMode')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
            <div className="max-w-full overflow-x-auto pb-0.5">
                <Tabs value={sortMode} onValueChange={(value) => setSortMode(value as RankSortMode)}>
                    <TabsList aria-label={t('sortLabel')}>
                        <TabsTrigger value="cost">{t('sortByCost')}</TabsTrigger>
                        <TabsTrigger value="count">{t('sortByCount')}</TabsTrigger>
                        <TabsTrigger value="tokens">{t('sortByTokens')}</TabsTrigger>
                        <TabsTrigger value="failed">{t('sortByFailed')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
        </div>
    );
}

function RankNotice({
    message,
    destructive = false,
}: {
    message: string;
    destructive?: boolean;
}) {
    const appearance = destructive
        ? 'bg-destructive/10 text-destructive'
        : 'bg-muted/50 text-muted-foreground';
    return (
        <div className={`mt-3 flex items-start gap-2 rounded-xl px-3 py-2 text-xs ${appearance}`} role="status">
            <AlertCircle className="mt-0.5 size-3.5 shrink-0" />
            <span>{message}</span>
        </div>
    );
}
