'use client';

import { useState } from 'react';
import { Settings2 } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';
import { Button } from '@/components/ui/button';
import { toast } from '@/components/common/Toast';
import {
    normalizeGroupProtocolMode,
    normalizePreferredProtocols,
    type Group,
    type GroupProtocolMode,
    type ProtocolName,
    useUpdateGroup,
} from '@/api/endpoints/group';
import { ProtocolPolicySection } from './ProtocolPolicySection';

export function ProtocolPolicyPopover({ group }: { group: Group }) {
    const t = useTranslations('group');
    const updateGroup = useUpdateGroup();
    const [open, setOpen] = useState(false);
    const [mode, setMode] = useState<GroupProtocolMode>('follow');
    const [preferredProtocols, setPreferredProtocols] = useState<ProtocolName[]>([]);

    const syncDraft = () => {
        setMode(normalizeGroupProtocolMode(group.protocol_mode));
        setPreferredProtocols(normalizePreferredProtocols(group.preferred_protocols));
    };

    const handleOpenChange = (nextOpen: boolean) => {
        setOpen(nextOpen);
        if (nextOpen) syncDraft();
    };

    const handleSave = () => {
        if (!group.id || updateGroup.isPending) return;
        const currentMode = normalizeGroupProtocolMode(group.protocol_mode);
        const currentPreferred = normalizePreferredProtocols(group.preferred_protocols);
        const changedMode = mode !== currentMode;
        const changedPreferred = JSON.stringify(preferredProtocols) !== JSON.stringify(currentPreferred);
        if (!changedMode && !changedPreferred) {
            setOpen(false);
            return;
        }

        updateGroup.mutate(
            {
                id: group.id,
                ...(changedMode ? { protocol_mode: mode } : {}),
                ...(changedPreferred ? { preferred_protocols: preferredProtocols } : {}),
            },
            {
                onSuccess: () => {
                    toast.success(t('toast.updated'));
                    setOpen(false);
                },
                onError: (error) => toast.error(t('toast.updateFailed'), { description: error.message }),
            },
        );
    };

    return (
        <Popover open={open} onOpenChange={handleOpenChange}>
            <PopoverTrigger asChild>
                <button
                    type="button"
                    disabled={!group.id}
                    className="p-1.5 rounded-lg transition-colors hover:bg-muted text-muted-foreground hover:text-foreground disabled:pointer-events-none disabled:opacity-50"
                    aria-label={t('protocol.quickEdit')}
                >
                    <Tooltip side="top" sideOffset={10} align="center">
                        <TooltipTrigger asChild>
                            <Settings2 className="size-4" />
                        </TooltipTrigger>
                        <TooltipContent>{t('protocol.quickEdit')}</TooltipContent>
                    </Tooltip>
                </button>
            </PopoverTrigger>
            <PopoverContent
                align="end"
                side="bottom"
                sideOffset={8}
                className="w-[min(26rem,calc(100vw-2rem))] max-h-[calc(100vh-5rem)] overflow-y-auto rounded-2xl border border-border/60 bg-card p-3 shadow-xl"
            >
                <div className="flex flex-col gap-3">
                    <div>
                        <p className="text-sm font-semibold">{t('protocol.quickEdit')}</p>
                        <p className="mt-0.5 text-[11px] text-muted-foreground">{group.name}</p>
                    </div>
                    <ProtocolPolicySection
                        mode={mode}
                        preferredProtocols={preferredProtocols}
                        onModeChange={setMode}
                        onPreferredChange={setPreferredProtocols}
                        defaultOpen
                    />
                    <Button
                        type="button"
                        className="h-9 w-full rounded-lg text-sm"
                        disabled={updateGroup.isPending}
                        onClick={handleSave}
                    >
                        {updateGroup.isPending ? t('create.submitting') : t('detail.actions.save')}
                    </Button>
                </div>
            </PopoverContent>
        </Popover>
    );
}
