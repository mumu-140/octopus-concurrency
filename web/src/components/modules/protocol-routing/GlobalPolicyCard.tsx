'use client';

import { Activity, RefreshCw } from 'lucide-react';
import type { ApiError } from '@/api/types';
import { useProtocolPolicy, useUpdateProtocolConfig } from '@/api/endpoints/protocol-routing';
import { toast } from '@/components/common/Toast';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { SettingCard, SettingRow } from '@/components/modules/setting/shared';

export function GlobalPolicyCard() {
    const policy = useProtocolPolicy();
    const update = useUpdateProtocolConfig();

    const save = (enabled: boolean) => {
        if (!policy.data) return;
        update.mutate(
            {
                expected_revision: policy.data.active_revision,
                protocol_routing_enabled: enabled,
            },
            {
                onSuccess: () => toast.success('协议路由开关已保存'),
                onError: (error) => {
                    const apiError = error as unknown as ApiError;
                    if (apiError.code === 409) {
                        toast.error('配置已被其他操作更新', { description: '请刷新后重试。' });
                        policy.refetch();
                        return;
                    }
                    toast.error('保存失败', { description: apiError.message });
                },
            },
        );
    };

    if (policy.isLoading) {
        return (
            <SettingCard icon={Activity} title="协议路由">
                <div className="h-20 animate-pulse rounded-2xl bg-muted" />
            </SettingCard>
        );
    }
    if (policy.isError || !policy.data) {
        return (
            <SettingCard icon={Activity} title="协议路由">
                <div className="rounded-2xl border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
                    无法加载协议路由开关。
                </div>
                <Button variant="outline" className="w-full rounded-xl" onClick={() => policy.refetch()}>
                    <RefreshCw className="size-4" />
                    重新加载
                </Button>
            </SettingCard>
        );
    }

    const enabled = policy.data.config.protocol_routing_enabled;
    return (
        <SettingCard
            icon={Activity}
            title="协议路由"
            tooltip="全局总开关。关闭后所有分组都按渠道默认协议执行；开启后按分组的跟随/覆盖/自动策略执行。"
        >
            <SettingRow label="启用分组协议策略">
                <Switch disabled={update.isPending} checked={enabled} onCheckedChange={save} />
            </SettingRow>
            <p className="rounded-2xl bg-muted/40 px-4 py-3 text-xs leading-5 text-muted-foreground">
                {enabled
                    ? '已开启：各模型分组可配置跟随渠道、覆盖或自动回退。'
                    : '已关闭：忽略分组协议配置，始终使用渠道默认协议。'}
            </p>
        </SettingCard>
    );
}
