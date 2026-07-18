'use client';

import { useState } from 'react';
import { ArrowDown, ArrowUp, ChevronDown } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';
import {
    PROTOCOL_OPTIONS,
    type GroupProtocolMode,
    type ProtocolName,
    protocolLabel,
} from '@/api/endpoints/group';

export function buildProtocolSummary(
    mode: GroupProtocolMode,
    preferred: ProtocolName[],
    labels: {
        follow: string;
        overrideEmpty: string;
        override: (list: string) => string;
        autoEmpty: string;
        auto: (list: string) => string;
    },
): string {
    if (mode === 'follow') return labels.follow;
    const names = preferred.map((protocol) => protocolLabel(protocol));
    if (mode === 'override') {
        if (names.length === 0) return labels.overrideEmpty;
        return labels.override(names.join(' → '));
    }
    if (names.length === 0) return labels.autoEmpty;
    return labels.auto(names.join(' → '));
}

export function ProtocolPolicySection({
    mode,
    preferredProtocols,
    onModeChange,
    onPreferredChange,
    disabled = false,
    defaultOpen = false,
}: {
    mode: GroupProtocolMode;
    preferredProtocols: ProtocolName[];
    onModeChange: (mode: GroupProtocolMode) => void;
    onPreferredChange: (protocols: ProtocolName[]) => void;
    disabled?: boolean;
    defaultOpen?: boolean;
}) {
    const t = useTranslations('group');
    const [open, setOpen] = useState(defaultOpen || mode !== 'follow');
    const modes: GroupProtocolMode[] = ['follow', 'override', 'auto'];
    const orderingEnabled = mode !== 'follow' && !disabled;

    const summary = buildProtocolSummary(mode, preferredProtocols, {
        follow: t('protocol.summary.follow'),
        overrideEmpty: t('protocol.summary.overrideEmpty'),
        override: (list) => t('protocol.summary.override', { list }),
        autoEmpty: t('protocol.summary.autoEmpty'),
        auto: (list) => t('protocol.summary.auto', { list }),
    });

    const modeLabel = t(`protocol.mode.${mode}`);

    const toggleProtocol = (protocol: ProtocolName) => {
        if (!orderingEnabled) return;
        if (preferredProtocols.includes(protocol)) {
            onPreferredChange(preferredProtocols.filter((item) => item !== protocol));
            return;
        }
        onPreferredChange([...preferredProtocols, protocol]);
    };

    const moveProtocol = (protocol: ProtocolName, direction: -1 | 1) => {
        if (!orderingEnabled) return;
        const index = preferredProtocols.indexOf(protocol);
        if (index < 0) return;
        const next = index + direction;
        if (next < 0 || next >= preferredProtocols.length) return;
        const copy = [...preferredProtocols];
        [copy[index], copy[next]] = [copy[next], copy[index]];
        onPreferredChange(copy);
    };

    return (
        <section className="rounded-xl border border-border/50 bg-muted/20 overflow-hidden shrink-0">
            <button
                type="button"
                onClick={() => setOpen((value) => !value)}
                className="flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors hover:bg-muted/40"
                aria-expanded={open}
                aria-label={open ? t('protocol.collapse') : t('protocol.expand')}
            >
                <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                        <p className="text-sm font-medium text-foreground">{t('protocol.title')}</p>
                        <span className="rounded-md bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                            {modeLabel}
                        </span>
                    </div>
                    {!open ? (
                        <p className="mt-0.5 truncate text-[11px] leading-4 text-muted-foreground">{summary}</p>
                    ) : (
                        <p className="mt-0.5 text-[11px] text-muted-foreground">{t('protocol.hint')}</p>
                    )}
                </div>
                <ChevronDown
                    className={cn(
                        'size-4 shrink-0 text-muted-foreground transition-transform',
                        open && 'rotate-180',
                    )}
                />
            </button>

            {open ? (
                <div className="space-y-3 border-t border-border/40 px-3 pb-3 pt-3">
                    <div className="flex gap-1">
                        {modes.map((item) => (
                            <button
                                key={item}
                                type="button"
                                disabled={disabled}
                                onClick={() => onModeChange(item)}
                                className={cn(
                                    'flex-1 py-1.5 text-xs rounded-lg transition-colors',
                                    mode === item ? 'bg-primary text-primary-foreground' : 'bg-muted hover:bg-muted/80',
                                    disabled && 'opacity-50 cursor-not-allowed',
                                )}
                            >
                                {t(`protocol.mode.${item}`)}
                            </button>
                        ))}
                    </div>

                    <div
                        className={cn('space-y-1.5', !orderingEnabled && 'opacity-50 pointer-events-none')}
                        aria-disabled={!orderingEnabled}
                    >
                        <p className="text-xs font-medium text-muted-foreground">{t('protocol.orderLabel')}</p>
                        <div className="flex flex-col gap-1.5">
                            {PROTOCOL_OPTIONS.map((protocol) => {
                                const selected = preferredProtocols.includes(protocol);
                                const rank = selected ? preferredProtocols.indexOf(protocol) + 1 : 0;
                                return (
                                    <div
                                        key={protocol}
                                        className={cn(
                                            'flex items-center gap-2 rounded-lg border px-2.5 py-1.5 text-xs transition-colors',
                                            selected
                                                ? 'border-primary/40 bg-primary/5 text-foreground'
                                                : 'border-border/60 bg-background text-muted-foreground',
                                        )}
                                    >
                                        <button
                                            type="button"
                                            disabled={!orderingEnabled}
                                            onClick={() => toggleProtocol(protocol)}
                                            className="flex-1 min-w-0 text-left"
                                        >
                                            <span className="font-medium">{protocolLabel(protocol)}</span>
                                            <span className="ml-2 text-[10px] opacity-70">
                                                {selected ? t('protocol.rank', { rank }) : t('protocol.unselected')}
                                            </span>
                                        </button>
                                        {selected ? (
                                            <div className="flex items-center gap-0.5 shrink-0">
                                                <button
                                                    type="button"
                                                    disabled={!orderingEnabled || rank <= 1}
                                                    onClick={() => moveProtocol(protocol, -1)}
                                                    className="p-1 rounded-md hover:bg-muted disabled:opacity-30"
                                                    aria-label={t('protocol.moveUp')}
                                                >
                                                    <ArrowUp className="size-3.5" />
                                                </button>
                                                <button
                                                    type="button"
                                                    disabled={!orderingEnabled || rank >= preferredProtocols.length}
                                                    onClick={() => moveProtocol(protocol, 1)}
                                                    className="p-1 rounded-md hover:bg-muted disabled:opacity-30"
                                                    aria-label={t('protocol.moveDown')}
                                                >
                                                    <ArrowDown className="size-3.5" />
                                                </button>
                                            </div>
                                        ) : null}
                                    </div>
                                );
                            })}
                        </div>
                    </div>

                    <p className="rounded-lg bg-muted/50 px-2.5 py-2 text-[11px] leading-5 text-muted-foreground">
                        {summary}
                    </p>
                </div>
            ) : null}
        </section>
    );
}
