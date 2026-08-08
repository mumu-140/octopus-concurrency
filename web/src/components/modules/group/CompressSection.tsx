'use client';

import { useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';
import { Switch } from '@/components/ui/switch';
import {
    compressConfigForTier,
    compressTierOf,
    type CompressOutputStyle,
    type CompressTier,
    type GroupCompressConfig,
} from '@/api/endpoints/group';

const TIERS: Exclude<CompressTier, 'custom'>[] = ['low', 'medium', 'high'];
const OUTPUT_STYLES: CompressOutputStyle[] = ['', 'terse-prose', 'terse-cjk'];

// 分组级请求压缩配置。生效还需设置页的全局 compress_master_enabled 开启。
// 档位(low/medium/high)映射到引擎组合;手动改动任一引擎开关后档位显示为 custom。
export function CompressSection({
    value,
    onChange,
}: {
    value: GroupCompressConfig | undefined;
    onChange: (cfg: GroupCompressConfig | undefined) => void;
}) {
    const t = useTranslations('group');
    const [open, setOpen] = useState(value?.enabled === true);

    const enabled = value?.enabled === true;
    const lite = value?.lite ?? true;                 // 默认最低档
    const headroom = value?.headroom ?? false;
    const outputStyle = value?.output_style ?? '';

    const current: GroupCompressConfig = { enabled, lite, headroom, output_style: outputStyle };
    const tier: CompressTier = compressTierOf(current);

    const applyTier = (next: Exclude<CompressTier, 'custom'>) => {
        onChange(compressConfigForTier(next));
    };
    const patch = (partial: Partial<GroupCompressConfig>) => {
        onChange({ ...current, ...partial });
    };
    const toggleEnabled = (checked: boolean) => {
        onChange(checked ? current : undefined); // 关闭 → undefined,提交时清除压缩配置
    };

    return (
        <section className="rounded-xl border border-border/50 bg-muted/20 overflow-hidden shrink-0">
            <button
                type="button"
                onClick={() => setOpen((v) => !v)}
                className="flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors hover:bg-muted/40"
                aria-expanded={open}
                aria-label={open ? t('form.compress.collapse') : t('form.compress.expand')}
            >
                <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                        <p className="text-sm font-medium text-foreground">{t('form.compress.title')}</p>
                        <span className="rounded-md bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                            {enabled ? t(`form.compress.tier.${tier}`) : t('form.compress.off')}
                        </span>
                    </div>
                    {!open ? (
                        <p className="mt-0.5 truncate text-[11px] leading-4 text-muted-foreground">
                            {enabled ? t('form.compress.summary') : t('form.compress.summaryOff')}
                        </p>
                    ) : (
                        <p className="mt-0.5 text-[11px] text-muted-foreground">{t('form.compress.hint')}</p>
                    )}
                </div>
                <ChevronDown
                    className={cn('size-4 shrink-0 text-muted-foreground transition-transform', open && 'rotate-180')}
                />
            </button>

            {open ? (
                <div className="space-y-3 border-t border-border/40 px-3 pb-3 pt-3">
                    <div className="flex items-center justify-between gap-3">
                        <span className="text-xs font-medium text-muted-foreground">
                            {t('form.compress.enabled.label')}
                        </span>
                        <Switch checked={enabled} onCheckedChange={toggleEnabled} />
                    </div>

                    <div className={cn('space-y-3', !enabled && 'opacity-50 pointer-events-none')} aria-disabled={!enabled}>
                        <div>
                            <p className="mb-1.5 text-xs font-medium text-muted-foreground">{t('form.compress.tier.label')}</p>
                            <div className="flex gap-1">
                                {TIERS.map((item) => (
                                    <button
                                        key={item}
                                        type="button"
                                        disabled={!enabled}
                                        onClick={() => applyTier(item)}
                                        className={cn(
                                            'flex-1 py-1.5 text-xs rounded-lg transition-colors',
                                            tier === item
                                                ? 'bg-primary text-primary-foreground'
                                                : 'bg-muted hover:bg-muted/80',
                                            !enabled && 'cursor-not-allowed',
                                        )}
                                    >
                                        {t(`form.compress.tier.${item}`)}
                                    </button>
                                ))}
                            </div>
                            {tier === 'custom' ? (
                                <p className="mt-1 text-[10px] text-muted-foreground">{t('form.compress.tier.custom')}</p>
                            ) : null}
                        </div>

                        <div className="space-y-1.5 rounded-lg border border-border/40 bg-muted/30 px-2.5 py-2">
                            <p className="text-xs font-medium text-muted-foreground">{t('form.compress.engines.label')}</p>

                            <div className="flex items-center justify-between gap-3">
                                <span className="text-xs text-foreground" title={t('form.compress.engines.lite.description')}>
                                    {t('form.compress.engines.lite.label')}
                                </span>
                                <Switch checked={lite} disabled={!enabled} onCheckedChange={(v) => patch({ lite: v })} />
                            </div>

                            <div className="flex items-center justify-between gap-3">
                                <span className="text-xs text-foreground" title={t('form.compress.engines.headroom.description')}>
                                    {t('form.compress.engines.headroom.label')}
                                </span>
                                <Switch checked={headroom} disabled={!enabled} onCheckedChange={(v) => patch({ headroom: v })} />
                            </div>

                            <div>
                                <p className="mb-1.5 text-xs text-foreground" title={t('form.compress.engines.outputStyle.description')}>
                                    {t('form.compress.engines.outputStyle.label')}
                                </p>
                                <div className="flex gap-1">
                                    {OUTPUT_STYLES.map((style) => (
                                        <button
                                            key={style === '' ? 'off' : style}
                                            type="button"
                                            disabled={!enabled}
                                            onClick={() => patch({ output_style: style })}
                                            className={cn(
                                                'flex-1 py-1 text-xs rounded-lg transition-colors',
                                                outputStyle === style
                                                    ? 'bg-primary text-primary-foreground'
                                                    : 'bg-muted hover:bg-muted/80',
                                                !enabled && 'cursor-not-allowed',
                                            )}
                                        >
                                            {t(`form.compress.engines.outputStyle.${style === '' ? 'off' : style}`)}
                                        </button>
                                    ))}
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            ) : null}
        </section>
    );
}
