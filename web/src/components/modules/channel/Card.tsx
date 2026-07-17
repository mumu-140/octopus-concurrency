import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
import { CheckCircle2, KeyRound, Layers3, RefreshCw, Route, XCircle } from 'lucide-react';
import type { StatsMetricsFormatted } from '@/api/endpoints/stats';
import { channelProtocolLabel, type Channel, useEnableChannel } from '@/api/endpoints/channel';
import { CardContent } from './CardContent';
import { useTranslations } from 'next-intl';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { toast } from '@/components/common/Toast';
import { cn } from '@/lib/utils';

export function Card({
    channel,
    stats,
    layout = 'grid',
    selected = false,
    onSelect,
}: {
    channel: Channel;
    stats: StatsMetricsFormatted;
    layout?: 'grid' | 'list';
    selected?: boolean;
    onSelect?: (selected: boolean) => void;
}) {
    const t = useTranslations('channel.card');
    const enableChannel = useEnableChannel();
    const models = new Set(
        [channel.model, channel.custom_model]
            .flatMap((value) => value.split(','))
            .map((value) => value.trim())
            .filter(Boolean),
    );
    const enabledKeys = channel.keys.filter((key) => key.enabled).length;

    const handleEnableChange = (checked: boolean) => {
        enableChannel.mutate(
            { id: channel.id, enabled: checked },
            {
                onSuccess: () => toast.success(checked ? t('toast.enabled') : t('toast.disabled')),
                onError: (error) => toast.error(error.message),
            },
        );
    };

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full text-left">
                <article
                    className={cn(
                        'flex min-w-0 flex-col gap-3 rounded-2xl border border-border/70 bg-card p-3 text-card-foreground transition-colors hover:border-primary/30 hover:bg-card/90',
                        selected && 'border-primary ring-2 ring-primary/20',
                        layout === 'list' && 'sm:grid sm:grid-cols-[minmax(13rem,1.25fr)_minmax(16rem,1fr)_minmax(14rem,0.9fr)] sm:items-center sm:gap-5',
                    )}
                >
                    <header className="flex min-w-0 items-start gap-2">
                        {onSelect ? (
                            <input
                                type="checkbox"
                                aria-label={`选择 ${channel.name}`}
                                checked={selected}
                                onChange={(event) => onSelect(event.target.checked)}
                                onClick={(event) => event.stopPropagation()}
                                className="mt-1 size-4 shrink-0 accent-primary"
                            />
                        ) : null}
                        <div className="min-w-0 flex-1">
                            <Tooltip side="top" sideOffset={8}>
                                <TooltipTrigger asChild>
                                    <h3 className="truncate text-base font-semibold">{channel.name}</h3>
                                </TooltipTrigger>
                                <TooltipContent>{channel.name}</TooltipContent>
                            </Tooltip>
                            <div className="mt-1 flex flex-wrap items-center gap-1.5">
                                <Badge variant="outline" className="h-5 max-w-full px-1.5 text-[10px] font-normal">
                                    <Route className="mr-1 size-3" />
                                    <span className="truncate">{channelProtocolLabel(channel.type)}</span>
                                </Badge>
                                {channel.managed ? <Badge variant="outline" className="h-5 border-amber-500/30 bg-amber-500/10 px-1.5 text-[10px] text-amber-700 dark:text-amber-300">站点投影</Badge> : null}
                            </div>
                        </div>
                        <Switch
                            checked={channel.enabled}
                            onCheckedChange={handleEnableChange}
                            disabled={enableChannel.isPending || channel.managed}
                            onClick={(event) => event.stopPropagation()}
                            aria-label={`${channel.enabled ? '停用' : '启用'}渠道 ${channel.name}`}
                        />
                    </header>

                    <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                        <span className="inline-flex items-center gap-1"><Layers3 className="size-3.5" />{models.size} 个模型</span>
                        <span className="inline-flex items-center gap-1"><KeyRound className="size-3.5" />{enabledKeys}/{channel.keys.length} Key</span>
                        <span className="inline-flex items-center gap-1"><RefreshCw className="size-3.5" />{channel.auto_sync ? '自动同步' : '手动模型'}</span>
                    </div>

                    <dl className="grid grid-cols-3 divide-x divide-border/70 border-y border-border/70 py-2 text-center text-xs">
                        <div className="px-1.5">
                            <dt className="text-muted-foreground">请求</dt>
                            <dd className="mt-0.5 font-semibold tabular-nums">{stats.request_count.formatted.value}{stats.request_count.formatted.unit}</dd>
                        </div>
                        <div className="px-1.5">
                            <dt className="inline-flex items-center gap-1 text-muted-foreground"><CheckCircle2 className="size-3 text-emerald-600 dark:text-emerald-300" />成功</dt>
                            <dd className="mt-0.5 font-semibold tabular-nums text-emerald-700 dark:text-emerald-300">{stats.request_success.formatted.value}</dd>
                        </div>
                        <div className="px-1.5">
                            <dt className="inline-flex items-center gap-1 text-muted-foreground"><XCircle className="size-3 text-destructive" />失败</dt>
                            <dd className="mt-0.5 font-semibold tabular-nums text-destructive">{stats.request_failed.formatted.value}</dd>
                        </div>
                    </dl>
                </article>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="w-full max-w-3xl rounded-3xl bg-card px-4 py-3 text-card-foreground max-h-[90vh] overflow-y-auto">
                    <CardContent channel={channel} stats={stats} />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
