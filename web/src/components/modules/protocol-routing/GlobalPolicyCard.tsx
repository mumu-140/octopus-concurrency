'use client';

import { useEffect, useState } from 'react';
import { Activity, RefreshCw } from 'lucide-react';
import type { ApiError } from '@/api/types';
import { useProtocolPolicy, useUpdateProtocolConfig, type RoutingMode } from '@/api/endpoints/protocol-routing';
import { toast } from '@/components/common/Toast';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { SettingCard, SettingRow, SettingSection } from '@/components/modules/setting/shared';

const MODE_LABELS: Record<RoutingMode, string> = {
    legacy: '兼容模式',
    observe: '观察模式',
    adaptive: '自适应模式',
};

export function GlobalPolicyCard() {
    const policy = useProtocolPolicy();
    const update = useUpdateProtocolConfig();
    const [allowlist, setAllowlist] = useState('');

    useEffect(() => {
        if (!policy.data) return;
        queueMicrotask(() => setAllowlist(policy.data.config.adaptive_group_allowlist.join(', ')));
    }, [policy.data]);

    const save = (patch: Record<string, unknown>) => {
        if (!policy.data) return;
        update.mutate(
            { expected_revision: policy.data.active_revision, ...patch },
            {
                onSuccess: () => toast.success('协议路由配置已保存'),
                onError: (error) => {
                    const apiError = error as unknown as ApiError;
                    if (apiError.code === 409) {
                        toast.error('配置已被其他操作更新', { description: '请刷新到最新 revision 后重试。' });
                        policy.refetch();
                        return;
                    }
                    toast.error('保存失败', { description: apiError.message });
                },
            },
        );
    };

    const saveAllowlist = () => {
        const values = allowlist.trim() === '' ? [] : allowlist.split(',').map((item) => Number(item.trim()));
        if (values.some((value) => !Number.isInteger(value) || value <= 0)) {
            toast.error('分组白名单格式错误', { description: '请输入逗号分隔的正整数分组 ID。' });
            return;
        }
        save({ adaptive_group_allowlist: [...new Set(values)] });
    };

    if (policy.isLoading) {
        return <SettingCard icon={Activity} title="模型协议路由"><div className="h-28 animate-pulse rounded-2xl bg-muted" /></SettingCard>;
    }
    if (policy.isError || !policy.data) {
        return (
            <SettingCard icon={Activity} title="模型协议路由">
                <div className="rounded-2xl border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">无法加载协议策略。</div>
                <Button variant="outline" className="w-full rounded-xl" onClick={() => policy.refetch()}><RefreshCw className="size-4" />重新加载</Button>
            </SettingCard>
        );
    }

    const { config, active_revision: revision } = policy.data;
    const disabled = update.isPending;
    return (
        <SettingCard icon={Activity} title="模型协议路由" tooltip="按 revision 管理协议选择；保存冲突时不会覆盖其他管理员的更新。">
            <div className="flex items-center justify-between rounded-2xl border bg-muted/30 px-4 py-3 text-sm">
                <span className="text-muted-foreground">当前配置版本</span>
                <span className="font-mono font-semibold">revision {revision}</span>
            </div>
            <SettingRow label="启用协议路由">
                <Switch disabled={disabled} checked={config.protocol_routing_enabled} onCheckedChange={(value) => save({ protocol_routing_enabled: value })} />
            </SettingRow>
            <SettingRow label="运行模式">
                <Select disabled={disabled || !config.protocol_routing_enabled} value={config.mode} onValueChange={(value) => save({ mode: value as RoutingMode })}>
                    <SelectTrigger className="w-40 rounded-xl"><SelectValue /></SelectTrigger>
                    <SelectContent>{Object.entries(MODE_LABELS).map(([value, label]) => <SelectItem key={value} value={value}>{label}</SelectItem>)}</SelectContent>
                </Select>
            </SettingRow>
            <SettingSection title="执行能力" />
            <SettingRow label="协议转换"><Switch disabled={disabled || !config.protocol_routing_enabled} checked={config.protocol_conversion_enabled} onCheckedChange={(value) => save({ protocol_conversion_enabled: value })} /></SettingRow>
            <SettingRow label="失败回退"><Switch disabled={disabled || !config.protocol_routing_enabled} checked={config.protocol_fallback_enabled} onCheckedChange={(value) => save({ protocol_fallback_enabled: value })} /></SettingRow>
            <SettingRow label="读取学习结果"><Switch disabled={disabled || !config.protocol_routing_enabled} checked={config.protocol_learning_read_enabled} onCheckedChange={(value) => save({ protocol_learning_read_enabled: value })} /></SettingRow>
            <SettingRow label="写入学习结果"><Switch disabled={disabled || !config.protocol_routing_enabled} checked={config.protocol_learning_write_enabled} onCheckedChange={(value) => save({ protocol_learning_write_enabled: value })} /></SettingRow>
            <SettingSection title="自适应分组白名单" tooltip="留空表示不限制；填写逗号分隔的分组 ID。" />
            <div className="flex flex-col gap-2 sm:flex-row">
                <Input disabled={disabled || !config.protocol_routing_enabled} value={allowlist} onChange={(event) => setAllowlist(event.target.value)} placeholder="例如：1, 3, 8" className="rounded-xl" />
                <Button disabled={disabled || !config.protocol_routing_enabled} onClick={saveAllowlist} className="rounded-xl sm:w-24">保存</Button>
            </div>
        </SettingCard>
    );
}
