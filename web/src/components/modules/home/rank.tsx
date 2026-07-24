'use client';

import { useMemo } from 'react';
import { useTranslations } from 'next-intl';
import { TrendingUp } from 'lucide-react';
import { Tabs, TabsList, TabsTrigger, TabsContents, TabsContent } from '@/components/animate-ui/components/animate/tabs';
import { useLeaderboard, type LeaderboardEntry } from '@/api/endpoints/stats';
import { useHomeViewStore, type RankSortMode, type RankGroupMode } from '@/components/modules/home/store';

export function Rank() {
    const t = useTranslations('home.rank');
    const rankSortMode = useHomeViewStore((state) => state.rankSortMode);
    const setRankSortMode = useHomeViewStore((state) => state.setRankSortMode);
    const rankGroupMode = useHomeViewStore((state) => state.rankGroupMode);
    const setRankGroupMode = useHomeViewStore((state) => state.setRankGroupMode);
    const period = useHomeViewStore((state) => state.chartPeriod);

    const { data: entries } = useLeaderboard(rankGroupMode, period);

    const rankedByCost = useMemo<LeaderboardEntry[]>(() => {
        if (!entries) return [];
        return [...entries].sort((a, b) => b.formatted.total_cost.raw - a.formatted.total_cost.raw);
    }, [entries]);

    const rankedByCount = useMemo<LeaderboardEntry[]>(() => {
        if (!entries) return [];
        return [...entries].sort((a, b) => b.formatted.request_count.raw - a.formatted.request_count.raw);
    }, [entries]);

    const rankedByTokens = useMemo<LeaderboardEntry[]>(() => {
        if (!entries) return [];
        return [...entries].sort((a, b) => b.formatted.total_token.raw - a.formatted.total_token.raw);
    }, [entries]);

    const rankedByFailed = useMemo<LeaderboardEntry[]>(() => {
        if (!entries) return [];
        return [...entries].sort((a, b) => b.formatted.request_failed.raw - a.formatted.request_failed.raw);
    }, [entries]);

    const getMedalEmoji = (rank: number): string => {
        switch (rank) {
            case 1: return '🥇';
            case 2: return '🥈';
            case 3: return '🥉';
            default: return '';
        }
    };

    const renderList = (rows: LeaderboardEntry[], mode: RankSortMode) => {
        if (rows.length === 0) {
            return (
                <div className="flex flex-col items-center justify-center py-6 text-muted-foreground">
                    <TrendingUp className="w-8 h-8 mb-2 opacity-30" />
                    <p className="text-sm">{t('noData')}</p>
                </div>
            );
        }
        return (
            <div className="space-y-1 max-h-[220px] overflow-y-auto">
                {rows.map((row, index) => {
                    const rank = index + 1;
                    const medal = getMedalEmoji(rank);

                    return (
                        <div
                            key={row.key}
                            className="flex items-center gap-2.5 px-2 py-1.5 rounded-xl hover:bg-accent/5 transition-colors"
                        >
                            <div className="w-6 h-6 rounded-md flex items-center justify-center font-bold text-sm shrink-0">
                                {medal || rank}
                            </div>

                            <div className="flex-1 min-w-0">
                                <p className="font-medium text-sm truncate leading-tight">{row.name}</p>
                                {mode === 'count' && (() => {
                                    const successCount = row.formatted.request_success.raw;
                                    const failedCount = row.formatted.request_failed.raw;
                                    const totalCount = successCount + failedCount;
                                    const successRate = totalCount > 0 ? (successCount / totalCount) * 100 : 0;

                                    return (
                                        <div className="flex items-center gap-1 text-xs text-muted-foreground leading-tight">
                                            <span>{t('successRate')}:</span>
                                            <span>{successRate.toFixed(1)}%</span>
                                        </div>
                                    );
                                })()}
                            </div>

                            <div className="flex items-center gap-1 text-right shrink-0">
                                {mode === 'count' ? (
                                    <div className="flex items-center gap-1 text-sm font-medium tabular-nums">
                                        <span className="text-accent">
                                            {row.formatted.request_success.formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {row.formatted.request_success.formatted.unit}
                                            </span>
                                        </span>
                                        <span className="text-muted-foreground/40 font-light">/</span>
                                        <span className="text-destructive">
                                            {row.formatted.request_failed.formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {row.formatted.request_failed.formatted.unit}
                                            </span>
                                        </span>
                                    </div>
                                ) : mode === 'failed' ? (
                                    <span className="font-semibold text-sm text-destructive">
                                        {row.formatted.request_failed.formatted.value}
                                        <span className="text-xs text-muted-foreground">
                                            {row.formatted.request_failed.formatted.unit}
                                        </span>
                                    </span>
                                ) : mode === 'tokens' ? (
                                    <span className="font-semibold text-sm">
                                        {row.formatted.total_token.formatted.value}
                                        <span className="text-xs text-muted-foreground">
                                            {row.formatted.total_token.formatted.unit}
                                        </span>
                                    </span>
                                ) : (
                                    <span className="font-semibold text-sm">
                                        {row.formatted.total_cost.formatted.value}
                                        <span className="text-xs text-muted-foreground">
                                            {row.formatted.total_cost.formatted.unit}
                                        </span>
                                    </span>
                                )}
                            </div>
                        </div>
                    );
                })}
            </div>
        );
    };

    return (
        <div className="rounded-3xl bg-card text-card-foreground border-card-border border px-4 py-3">
            <div className="flex items-center justify-between gap-2 flex-wrap">
                <div className="flex items-center gap-2">
                    <h3 className="font-semibold text-sm">{t('title')}</h3>
                    <Tabs value={rankGroupMode} onValueChange={(value) => setRankGroupMode(value as RankGroupMode)}>
                        <TabsList>
                            <TabsTrigger value="channel">{t('channelMode')}</TabsTrigger>
                            <TabsTrigger value="group">{t('groupMode')}</TabsTrigger>
                        </TabsList>
                    </Tabs>
                </div>
                <Tabs value={rankSortMode} onValueChange={(value) => setRankSortMode(value as RankSortMode)}>
                    <TabsList>
                        <TabsTrigger value="cost">{t('sortByCost')}</TabsTrigger>
                        <TabsTrigger value="count">{t('sortByCount')}</TabsTrigger>
                        <TabsTrigger value="tokens">{t('sortByTokens')}</TabsTrigger>
                        <TabsTrigger value="failed">{t('sortByFailed')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
            <Tabs value={rankSortMode} onValueChange={(value) => setRankSortMode(value as RankSortMode)} className="mt-2">
                <TabsContents>
                    <TabsContent value="cost">
                        {renderList(rankedByCost, 'cost')}
                    </TabsContent>
                    <TabsContent value="count">
                        {renderList(rankedByCount, 'count')}
                    </TabsContent>
                    <TabsContent value="tokens">
                        {renderList(rankedByTokens, 'tokens')}
                    </TabsContent>
                    <TabsContent value="failed">
                        {renderList(rankedByFailed, 'failed')}
                    </TabsContent>
                </TabsContents>
            </Tabs>
        </div>
    );
}
