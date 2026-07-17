import { Gauge, Globe2, KeyRound, Layers3, RefreshCw, Route } from 'lucide-react';
import { channelProtocolLabel, type Channel, type ChannelSyncResult } from '@/api/endpoints/channel';
import type { StatsMetricsFormatted } from '@/api/endpoints/stats';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { useChannelSyncStore } from '@/stores/channel-sync';

function modelNames(channel: Channel) {
    return new Set(
        [channel.model, channel.custom_model]
            .flatMap((value) => value.split(','))
            .map((value) => value.trim())
            .filter(Boolean),
    );
}

function compactURL(value: string) {
    try {
        const url = new URL(value);
        return `${url.host}${url.pathname === '/' ? '' : url.pathname}`;
    } catch {
        return value;
    }
}

function syncStatus(result: ChannelSyncResult | undefined) {
    if (!result) return { label: '未参与', tone: 'border-border bg-muted/50 text-muted-foreground' };
    switch (result.status) {
        case 'updated':
            return { label: '已变更', tone: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' };
        case 'failed':
            return { label: '失败', tone: 'border-destructive/30 bg-destructive/10 text-destructive' };
        default:
            return { label: '无变化', tone: 'border-border bg-muted/50 text-muted-foreground' };
    }
}

function ModelDelta({ label, values, tone }: { label: string; values: string[]; tone: string }) {
    if (values.length === 0) return null;
    const visible = values.slice(0, 6);
    const hidden = values.length - visible.length;
    return (
        <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
            <span className="shrink-0 text-muted-foreground">{label}</span>
            {visible.map((model) => (
                <Badge key={model} variant="outline" className={cn('h-5 min-w-0 max-w-[16rem] px-1.5 text-[10px] font-normal', tone)}>
                    <span className="truncate">{model}</span>
                </Badge>
            ))}
            {hidden > 0 ? <span className="text-[10px] text-muted-foreground">+{hidden}</span> : null}
        </div>
    );
}

export function ChannelOverview({ channel, stats }: { channel: Channel; stats: StatsMetricsFormatted }) {
    const report = useChannelSyncStore((state) => state.lastReport);
    const result = report?.results.find((item) => item.channel_id === channel.id);
    const sync = syncStatus(result);
    const models = modelNames(channel);
    const enabledKeys = channel.keys.filter((key) => key.enabled).length;

    return (
        <div className="space-y-4">
            <section className="grid grid-cols-3 divide-x divide-border/70 border-y border-border/70 py-2 text-center">
                <div className="px-2">
                    <div className="text-[11px] text-muted-foreground">请求</div>
                    <div className="mt-0.5 text-sm font-semibold tabular-nums">{stats.request_count.formatted.value}{stats.request_count.formatted.unit}</div>
                </div>
                <div className="px-2">
                    <div className="text-[11px] text-muted-foreground">成功 / 失败</div>
                    <div className="mt-0.5 text-sm font-semibold tabular-nums text-foreground">
                        <span className="text-emerald-700 dark:text-emerald-300">{stats.request_success.formatted.value}</span>
                        <span className="mx-1 text-muted-foreground">/</span>
                        <span className="text-destructive">{stats.request_failed.formatted.value}</span>
                    </div>
                </div>
                <div className="px-2">
                    <div className="text-[11px] text-muted-foreground">费用</div>
                    <div className="mt-0.5 text-sm font-semibold tabular-nums">{stats.total_cost.formatted.value}{stats.total_cost.formatted.unit}</div>
                </div>
            </section>

            <section aria-label="渠道配置" className="space-y-3">
                <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Route className="size-4 text-primary" />
                    渠道配置
                </div>
                <dl className="grid gap-x-5 gap-y-3 text-sm sm:grid-cols-2">
                    <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-2">
                        <dt className="text-muted-foreground">协议</dt>
                        <dd className="min-w-0 truncate text-right font-medium">{channelProtocolLabel(channel.type)}</dd>
                    </div>
                    <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-2">
                        <dt className="text-muted-foreground">WebSocket</dt>
                        <dd className="font-medium">{channel.ws_mode === 'inherit' ? '继承全局' : channel.ws_mode}</dd>
                    </div>
                    <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-2">
                        <dt className="inline-flex items-center gap-1.5 text-muted-foreground"><Layers3 className="size-3.5" />模型</dt>
                        <dd className="font-medium tabular-nums">{models.size} 个</dd>
                    </div>
                    <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-2">
                        <dt className="inline-flex items-center gap-1.5 text-muted-foreground"><KeyRound className="size-3.5" />密钥</dt>
                        <dd className="font-medium tabular-nums">{enabledKeys}/{channel.keys.length}</dd>
                    </div>
                    <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-2">
                        <dt className="inline-flex items-center gap-1.5 text-muted-foreground"><Gauge className="size-3.5" />限流</dt>
                        <dd className="font-medium tabular-nums">{channel.max_concurrency} 并发 · {channel.max_rpm || '不限'} RPM</dd>
                    </div>
                    <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-2">
                        <dt className="inline-flex items-center gap-1.5 text-muted-foreground"><RefreshCw className="size-3.5" />模型同步</dt>
                        <dd><Badge variant="outline" className="h-5 px-1.5 text-[10px]">{channel.auto_sync ? '自动' : '手动'}</Badge></dd>
                    </div>
                </dl>
            </section>

            <section aria-label="上游端点" className="border-t border-border/70 pt-3">
                <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Globe2 className="size-4 text-primary" />
                    上游端点
                </div>
                {channel.base_urls.length > 0 ? (
                    <ul className="divide-y divide-border/60 border-y border-border/60">
                        {channel.base_urls.map((endpoint) => (
                            <li key={endpoint.url} className="flex min-w-0 items-center justify-between gap-3 py-2 text-xs">
                                <span className="min-w-0 truncate font-mono text-foreground" title={endpoint.url}>{compactURL(endpoint.url)}</span>
                                <span className="shrink-0 tabular-nums text-muted-foreground">{endpoint.delay} ms</span>
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="text-xs text-muted-foreground">未配置上游端点。</p>
                )}
            </section>

            {report ? (
                <section aria-label="最近模型同步" className="border-t border-border/70 pt-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                            <RefreshCw className="size-4 text-primary" />
                            最近模型同步
                        </div>
                        <Badge variant="outline" className={cn('h-5 px-1.5 text-[10px]', sync.tone)}>{sync.label}</Badge>
                    </div>
                    <div className="mt-2 space-y-2">
                        {result ? (
                            <>
                                <ModelDelta label={result.status === 'failed' ? '检测到新增' : '新增'} values={result.added_models} tone="border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" />
                                <ModelDelta label={result.status === 'failed' ? '检测到移除' : '移除'} values={result.removed_models} tone="border-destructive/30 bg-destructive/10 text-destructive" />
                                {result.status === 'unchanged' ? <p className="text-xs text-muted-foreground">模型列表与同步前一致。</p> : null}
                                {result.error ? <p className="border-l-2 border-destructive/60 pl-2 text-xs leading-5 text-destructive">{result.error}</p> : null}
                            </>
                        ) : (
                            <p className="text-xs text-muted-foreground">该渠道未启用自动同步，未参与本次任务。</p>
                        )}
                    </div>
                </section>
            ) : null}
        </div>
    );
}
