import { useState, type FormEvent } from 'react';
import { Pencil, Route, Trash2 } from 'lucide-react';
import {
    channelProtocolLabel,
    type Channel,
    type UpdateChannelRequest,
    useDeleteChannel,
    useUpdateChannel,
} from '@/api/endpoints/channel';
import {
    MorphingDialogClose,
    MorphingDialogDescription,
    MorphingDialogTitle,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { Tabs, TabsContent, TabsContents } from '@/components/animate-ui/primitives/animate/tabs';
import type { StatsMetricsFormatted } from '@/api/endpoints/stats';
import { useTranslations } from 'next-intl';
import { toast } from '@/components/common/Toast';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ChannelForm, type ChannelFormData } from './Form';
import { ChannelFailureHistory } from './ChannelFailureHistory';
import { ChannelOverview } from './ChannelOverview';
import { useJumpStore } from '@/stores/jump';
import { ChannelPolicyPanel } from '@/components/modules/protocol-routing/ChannelPolicyPanel';

export function CardContent({ channel, stats }: { channel: Channel; stats: StatsMetricsFormatted }) {
    const { setIsOpen, isOpen } = useMorphingDialog();
    const updateChannel = useUpdateChannel();
    const deleteChannel = useDeleteChannel();
    const requestJump = useJumpStore((state) => state.requestJump);
    const [isEditing, setIsEditing] = useState(false);
    const [isProtocolEditing, setIsProtocolEditing] = useState(false);
    const [isConfirmingDelete, setIsConfirmingDelete] = useState(false);
    const [formData, setFormData] = useState<ChannelFormData>({
        name: channel.name,
        type: channel.type,
        enabled: channel.enabled,
        max_concurrency: channel.max_concurrency ?? 3,
        max_rpm: channel.max_rpm ?? 0,
        base_urls: channel.base_urls?.length ? channel.base_urls : [{ url: '', delay: 0 }],
        custom_header: channel.custom_header ?? [],
        ws_mode: channel.ws_mode ?? 'inherit',
        proxy_mode: channel.proxy_mode ?? 'direct',
        proxy_config_id: channel.proxy_config_id ?? null,
        param_override: channel.param_override ?? '',
        keys: channel.keys.length > 0
            ? channel.keys.map((key) => ({
                id: key.id,
                enabled: key.enabled,
                channel_key: key.channel_key,
                status_code: key.status_code,
                last_use_time_stamp: key.last_use_time_stamp,
                total_cost: key.total_cost,
                remark: key.remark,
            }))
            : [{ enabled: true, channel_key: '', remark: '' }],
        model: channel.model,
        custom_model: channel.custom_model,
        auto_sync: channel.auto_sync,
        auto_group: channel.auto_group,
        match_regex: channel.match_regex ?? '',
    });
    const t = useTranslations('channel.detail');
    const tProxy = useTranslations('proxyPool');
    const currentView = isProtocolEditing ? 'protocol' : isEditing ? 'editing' : 'viewing';

    const baseURLsEqual = (left: Channel['base_urls'] | undefined, right: Channel['base_urls'] | undefined) =>
        JSON.stringify(left ?? []) === JSON.stringify(right ?? []);
    const headersEqual = (left: Channel['custom_header'] | undefined, right: Channel['custom_header'] | undefined) =>
        JSON.stringify(left ?? []) === JSON.stringify(right ?? []);

    const handleUpdate = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        const request: UpdateChannelRequest = { id: channel.id };

        if (formData.name !== channel.name) request.name = formData.name;
        if (formData.type !== channel.type) request.type = formData.type;
        if (formData.enabled !== channel.enabled) request.enabled = formData.enabled;
        if (formData.max_concurrency !== (channel.max_concurrency ?? 3)) request.max_concurrency = formData.max_concurrency;
        if (formData.max_rpm !== (channel.max_rpm ?? 0)) request.max_rpm = formData.max_rpm;
        if (!baseURLsEqual(formData.base_urls, channel.base_urls)) {
            request.base_urls = (formData.base_urls ?? [])
                .filter((endpoint) => endpoint.url.trim())
                .map((endpoint) => ({ url: endpoint.url.trim(), delay: Number(endpoint.delay || 0) }));
        }
        if (formData.model !== channel.model) request.model = formData.model;
        if (formData.custom_model !== channel.custom_model) request.custom_model = formData.custom_model;
        if (formData.proxy_mode === 'pool' && !formData.proxy_config_id) {
            toast.error(tProxy('selectRequired'));
            return;
        }
        if (formData.proxy_mode !== channel.proxy_mode) request.proxy_mode = formData.proxy_mode;
        if ((formData.proxy_config_id ?? null) !== (channel.proxy_config_id ?? null) || formData.proxy_mode !== channel.proxy_mode) {
            request.proxy_config_id = formData.proxy_mode === 'pool' ? formData.proxy_config_id : null;
        }
        if (formData.auto_sync !== channel.auto_sync) request.auto_sync = formData.auto_sync;
        if (formData.auto_group !== channel.auto_group) request.auto_group = formData.auto_group;
        if ((formData.ws_mode ?? 'inherit') !== (channel.ws_mode ?? 'inherit')) request.ws_mode = formData.ws_mode;
        if (!headersEqual(formData.custom_header, channel.custom_header)) {
            request.custom_header = (formData.custom_header ?? [])
                .map((header) => ({ header_key: header.header_key.trim(), header_value: header.header_value }))
                .filter((header) => header.header_key && header.header_value !== '');
        }

        const nextParamOverride = formData.param_override.trim();
        if (nextParamOverride !== (channel.param_override ?? '')) request.param_override = nextParamOverride;
        const nextMatchRegex = formData.match_regex.trim();
        if (nextMatchRegex !== (channel.match_regex ?? '')) request.match_regex = nextMatchRegex;

        const originalKeysByID = new Map(channel.keys.map((key) => [key.id, key]));
        const nextKeys = formData.keys ?? [];
        const nextIDs = new Set(nextKeys.flatMap((key) => typeof key.id === 'number' ? [key.id] : []));
        const keysToDelete = channel.keys.filter((key) => !nextIDs.has(key.id)).map((key) => key.id);
        const keysToAdd = nextKeys
            .filter((key) => !key.id && key.channel_key.trim())
            .map((key) => ({ enabled: key.enabled, channel_key: key.channel_key, remark: key.remark ?? '' }));
        const keysToUpdate = nextKeys.flatMap((key) => {
            if (typeof key.id !== 'number') return [];
            const original = originalKeysByID.get(key.id);
            if (!original) return [];
            const update: { id: number; enabled?: boolean; channel_key?: string; remark?: string } = { id: key.id };
            if (key.enabled !== original.enabled) update.enabled = key.enabled;
            if (key.channel_key !== original.channel_key) update.channel_key = key.channel_key;
            if ((key.remark ?? '') !== original.remark) update.remark = key.remark ?? '';
            return Object.keys(update).length > 1 ? [update] : [];
        });
        if (keysToAdd.length > 0) request.keys_to_add = keysToAdd;
        if (keysToUpdate.length > 0) request.keys_to_update = keysToUpdate;
        if (keysToDelete.length > 0) request.keys_to_delete = keysToDelete;

        updateChannel.mutate(request, {
            onSuccess: () => {
                setIsEditing(false);
                setIsOpen(false);
            },
        });
    };

    const handleDelete = () => {
        if (!isConfirmingDelete) {
            setIsConfirmingDelete(true);
            return;
        }
        setIsOpen(false);
        window.setTimeout(() => deleteChannel.mutate(channel.id), 300);
    };

    const jumpToManagedSource = (target: 'site' | 'site-channel') => {
        if (!channel.managed_source) return;
        setIsOpen(false);
        requestJump(target === 'site'
            ? { kind: 'site-account', siteId: channel.managed_source.site_id, accountId: channel.managed_source.site_account_id }
            : { kind: 'site-channel-account', siteId: channel.managed_source.site_id, accountId: channel.managed_source.site_account_id });
    };

    return (
        <>
            <MorphingDialogTitle>
                <header className="mb-4 flex items-start justify-between gap-4 border-b border-border/70 pb-3">
                    <div className="min-w-0">
                        <h2 className="truncate text-xl font-bold text-card-foreground">{isEditing ? t('title.edit') : channel.name}</h2>
                        <div className="mt-1 flex flex-wrap items-center gap-2">
                            <Badge variant="outline" className="h-5 px-1.5 text-[10px]">{channelProtocolLabel(channel.type)}</Badge>
                            {channel.managed ? <Badge variant="outline" className="h-5 border-amber-500/30 bg-amber-500/10 px-1.5 text-[10px] text-amber-700 dark:text-amber-300">站点投影</Badge> : null}
                        </div>
                    </div>
                    <MorphingDialogClose className="relative top-0 right-0 shrink-0" />
                </header>
            </MorphingDialogTitle>

            <MorphingDialogDescription>
                <Tabs value={currentView}>
                    <TabsContents>
                        <TabsContent value="viewing">
                            <div className="max-h-[68vh] space-y-4 overflow-y-auto pr-1">
                                {channel.managed ? (
                                    <section className="border-l-2 border-amber-500/70 bg-amber-500/5 px-3 py-2 text-xs leading-5 text-amber-900 dark:text-amber-100">
                                        <p>这是站点账号自动投影的托管渠道，请在站点管理中修改配置，避免后续同步覆盖。</p>
                                        {channel.managed_source ? (
                                            <div className="mt-2 flex flex-wrap gap-2">
                                                <Button type="button" variant="outline" size="sm" className="h-8 rounded-lg" onClick={() => jumpToManagedSource('site')}>查看来源站点</Button>
                                                <Button type="button" variant="outline" size="sm" className="h-8 rounded-lg" onClick={() => jumpToManagedSource('site-channel')}>查看站点渠道</Button>
                                            </div>
                                        ) : null}
                                    </section>
                                ) : null}
                                <ChannelOverview channel={channel} stats={stats} />
                                <ChannelFailureHistory channelId={channel.id} enabled={isOpen} />
                            </div>

                            {!channel.managed ? (
                                <div className="mt-4 grid gap-2 border-t border-border/70 pt-3 sm:grid-cols-3">
                                    <Button type="button" variant="default" className="h-10 rounded-xl" onClick={() => setIsEditing(true)}>
                                        <Pencil className="size-4" />
                                        {t('actions.edit')}
                                    </Button>
                                    <Button type="button" variant="outline" className="h-10 rounded-xl" onClick={() => setIsProtocolEditing(true)}>
                                        <Route className="size-4" />
                                        协议策略
                                    </Button>
                                    <Button type="button" variant={isConfirmingDelete ? 'destructive' : 'outline'} disabled={deleteChannel.isPending} className="h-10 rounded-xl" onClick={handleDelete}>
                                        <Trash2 className="size-4" />
                                        {deleteChannel.isPending ? t('actions.deleting') : isConfirmingDelete ? t('actions.confirmDelete') : t('actions.delete')}
                                    </Button>
                                </div>
                            ) : null}
                        </TabsContent>

                        <TabsContent value="editing">
                            <ChannelForm
                                formData={formData}
                                onFormDataChange={setFormData}
                                onSubmit={handleUpdate}
                                isPending={updateChannel.isPending}
                                submitText={t('actions.save')}
                                pendingText={t('actions.saving')}
                                onCancel={() => setIsEditing(false)}
                                cancelText={t('actions.cancel')}
                                idPrefix="channel"
                            />
                        </TabsContent>

                        <TabsContent value="protocol">
                            <ChannelPolicyPanel channelId={channel.id} channelKeys={channel.keys} onBack={() => setIsProtocolEditing(false)} />
                        </TabsContent>
                    </TabsContents>
                </Tabs>
            </MorphingDialogDescription>
        </>
    );
}
