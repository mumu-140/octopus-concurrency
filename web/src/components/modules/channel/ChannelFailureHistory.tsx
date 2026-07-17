import { AlertCircle, Clock3, LoaderCircle } from 'lucide-react';
import { useLogPage, type RelayLog } from '@/api/endpoints/log';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

function failureReason(log: RelayLog) {
    const requestError = log.error?.trim();
    if (requestError) return requestError;

    const failedAttempt = [...(log.attempts ?? [])]
        .reverse()
        .find((attempt) => attempt.status !== 'success' && attempt.msg?.trim());
    if (failedAttempt?.msg?.trim()) return failedAttempt.msg.trim();

    const lastAttempt = [...(log.attempts ?? [])].reverse().find((attempt) => attempt.status !== 'success');
    if (lastAttempt) return `渠道尝试状态：${lastAttempt.status}`;
    return '未记录上游失败原因';
}

function formatLogTime(unixSeconds: number) {
    if (!unixSeconds) return '时间未知';
    return new Date(unixSeconds * 1000).toLocaleString();
}

export function ChannelFailureHistory({ channelId, enabled }: { channelId: number; enabled: boolean }) {
    const { data, isLoading, error } = useLogPage({
        page: 1,
        page_size: 5,
        channel_ids: [channelId],
        status: 'error',
        include_content: false,
        with_total: true,
        enabled,
    });

    const logs = data?.logs ?? [];
    const total = data?.total ?? 0;

    return (
        <section aria-label="失败历史" className="min-w-0 border-t border-border/70 pt-3">
            <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <AlertCircle className="size-4 text-destructive" />
                    失败历史
                </div>
                <Badge variant="outline" className="h-5 px-1.5 text-[10px] tabular-nums">
                    {isLoading ? '加载中' : `${total} 条`}
                </Badge>
            </div>

            {isLoading ? (
                <div className="mt-2 flex items-center gap-2 py-3 text-xs text-muted-foreground">
                    <LoaderCircle className="size-3.5 animate-spin" />
                    正在获取最近失败记录
                </div>
            ) : error ? (
                <div className="mt-2 border-l-2 border-destructive/60 pl-2 text-xs leading-5 text-destructive">
                    无法加载失败历史：{error.message}
                </div>
            ) : logs.length === 0 ? (
                <div className="mt-2 py-3 text-xs text-muted-foreground">未找到该渠道的失败请求。</div>
            ) : (
                <ol className="mt-2 divide-y divide-border/60 border-y border-border/60">
                    {logs.map((log) => (
                        <li key={log.id} className="py-2.5">
                            <div className="flex min-w-0 items-center justify-between gap-3 text-xs">
                                <span className="min-w-0 truncate font-medium text-foreground">
                                    {log.actual_model_name || log.request_model_name || '未标记模型'}
                                </span>
                                <span className="inline-flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
                                    <Clock3 className="size-3" />
                                    {formatLogTime(log.time)}
                                </span>
                            </div>
                            <p
                                className={cn(
                                    'mt-1 break-words text-xs leading-5 text-muted-foreground',
                                    !log.error?.trim() && 'text-amber-700 dark:text-amber-300',
                                )}
                                title={failureReason(log)}
                            >
                                {failureReason(log)}
                            </p>
                        </li>
                    ))}
                </ol>
            )}
        </section>
    );
}
