import type { LeaderboardEntry } from '@/api/endpoints/stats';
import {
    LeaderboardRow,
    type RankTranslate,
} from '@/components/modules/home/rank-row';
import type { RankSortMode } from '@/components/modules/home/store';
import { AlertCircle, ChevronLeft, ChevronRight, TrendingUp } from 'lucide-react';
import { useState, type ReactNode } from 'react';

export const RANK_PAGE_SIZE = 8;

export function LeaderboardRows({
    rows,
    mode,
    initialLoading,
    showBlockingError,
    t,
}: {
    rows: LeaderboardEntry[];
    mode: RankSortMode;
    initialLoading: boolean;
    showBlockingError: boolean;
    t: RankTranslate;
}) {
    const [page, setPage] = useState(0);
    const pageCount = Math.max(1, Math.ceil(rows.length / RANK_PAGE_SIZE));
    const currentPage = Math.min(page, pageCount - 1);
    const start = currentPage * RANK_PAGE_SIZE;
    const visibleRows = rows.slice(start, start + RANK_PAGE_SIZE);
    return (
        <>
            <LeaderboardContent
                rows={visibleRows}
                rankOffset={start}
                mode={mode}
                initialLoading={initialLoading}
                showBlockingError={showBlockingError}
                t={t}
            />
            {rows.length > RANK_PAGE_SIZE && (
                <LeaderboardPagination
                    page={currentPage}
                    pageCount={pageCount}
                    setPage={setPage}
                    t={t}
                />
            )}
        </>
    );
}

function LeaderboardContent({
    rows,
    rankOffset,
    mode,
    initialLoading,
    showBlockingError,
    t,
}: {
    rows: LeaderboardEntry[];
    rankOffset: number;
    mode: RankSortMode;
    initialLoading: boolean;
    showBlockingError: boolean;
    t: RankTranslate;
}) {
    if (initialLoading) return <LeaderboardSkeleton label={t('loading')} />;
    if (showBlockingError) return <LeaderboardEmpty message={t('error')} error />;
    if (rows.length === 0) return <LeaderboardEmpty message={t('noData')} />;
    return (
        <div className="mt-3 min-h-[260px] space-y-2">
            {rows.map((row, index) => (
                <LeaderboardRow
                    key={row.key}
                    row={row}
                    rank={rankOffset + index + 1}
                    mode={mode}
                    t={t}
                />
            ))}
        </div>
    );
}

function LeaderboardSkeleton({ label }: { label: string }) {
    return (
        <div className="mt-3 min-h-[260px] space-y-2" aria-label={label} role="status">
            {Array.from({ length: 5 }, (_, index) => (
                <div
                    key={index}
                    className="h-11 animate-pulse motion-reduce:animate-none rounded-xl bg-muted/50"
                />
            ))}
        </div>
    );
}

function LeaderboardEmpty({ message, error = false }: { message: string; error?: boolean }) {
    return (
        <div
            className="mt-3 flex min-h-[260px] flex-col items-center justify-center gap-2 text-muted-foreground"
            role={error ? 'alert' : undefined}
        >
            {error
                ? <AlertCircle className="size-8 opacity-40" />
                : <TrendingUp className="size-10 opacity-30" />}
            <p className="text-sm">{message}</p>
        </div>
    );
}

function LeaderboardPagination({
    page,
    pageCount,
    setPage,
    t,
}: {
    page: number;
    pageCount: number;
    setPage: (page: number) => void;
    t: RankTranslate;
}) {
    return (
        <footer className="mt-3 flex items-center justify-between border-t border-border/60 pt-3">
            <span className="text-xs text-muted-foreground">
                {t('page', { current: page + 1, total: pageCount })}
            </span>
            <div className="flex items-center gap-1">
                <PaginationButton
                    label={t('previous')}
                    disabled={page === 0}
                    onClick={() => setPage(Math.max(0, page - 1))}
                    icon={<ChevronLeft className="size-4" />}
                />
                <PaginationButton
                    label={t('next')}
                    disabled={page >= pageCount - 1}
                    onClick={() => setPage(Math.min(pageCount - 1, page + 1))}
                    icon={<ChevronRight className="size-4" />}
                />
            </div>
        </footer>
    );
}

function PaginationButton({
    label,
    disabled,
    onClick,
    icon,
}: {
    label: string;
    disabled: boolean;
    onClick: () => void;
    icon: ReactNode;
}) {
    return (
        <button
            type="button"
            disabled={disabled}
            onClick={onClick}
            className="inline-flex size-11 items-center justify-center rounded-lg text-muted-foreground hover:bg-accent/10 disabled:pointer-events-none disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={label}
            title={label}
        >
            {icon}
        </button>
    );
}
