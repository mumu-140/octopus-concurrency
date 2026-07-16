'use client';

import { useEffect, useState } from 'react';
import { Route } from 'lucide-react';
import type { ApiError } from '@/api/types';
import { useProtocolPolicy, useUpdateScopedProtocolPolicy, type PolicyMode, type Protocol } from '@/api/endpoints/protocol-routing';
import { toast } from '@/components/common/Toast';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

const PROTOCOLS: Protocol[] = ['openai_chat', 'openai_response', 'anthropic'];

function ScopedPolicyContent({ kind, id, name }: { kind: 'groups' | 'group-presets'; id: number; name: string }) {
    const { setIsOpen } = useMorphingDialog();
    const policy = useProtocolPolicy();
    const mutation = useUpdateScopedProtocolPolicy(kind, id);
    const [mode, setMode] = useState<PolicyMode>('inherit');
    const [protocols, setProtocols] = useState<Protocol[]>([]);

    const current = policy.data && (kind === 'groups'
        ? policy.data.groups.find((item) => item.group_id === id)
        : policy.data.group_presets.find((item) => item.group_preset_id === id));

    useEffect(() => {
        if (!policy.data) return;
        queueMicrotask(() => {
            setMode(current?.mode ?? 'inherit');
            setProtocols(current?.preferred_protocols ?? []);
        });
    }, [current, policy.data]);

    const toggleProtocol = (protocol: Protocol) => {
        setProtocols((items) => items.includes(protocol) ? items.filter((item) => item !== protocol) : [...items, protocol]);
    };
    const save = () => {
        if (!policy.data) return;
        if ((mode === 'prefer' || mode === 'force') && protocols.length === 0) {
            toast.error('优先或强制模式至少选择一个协议');
            return;
        }
        mutation.mutate(
            { expected_revision: policy.data.active_revision, mode, preferred_protocols: mode === 'inherit' || mode === 'auto' ? [] : protocols },
            {
                onSuccess: () => { toast.success('协议策略已保存'); setIsOpen(false); },
                onError: (error) => {
                    const apiError = error as unknown as ApiError;
                    if (apiError.code === 409) {
                        toast.error('配置已更新，请刷新后重试');
                        policy.refetch();
                    } else toast.error('保存失败', { description: apiError.message });
                },
            },
        );
    };

    return (
        <>
            <MorphingDialogTitle><header className="mb-5 flex items-center justify-between"><div><h2 className="text-xl font-bold">协议路由策略</h2><p className="mt-1 text-sm text-muted-foreground">{name}</p></div><MorphingDialogClose className="relative right-0 top-0" /></header></MorphingDialogTitle>
            <MorphingDialogDescription className="space-y-5">
                {policy.isLoading && <div className="h-40 animate-pulse rounded-2xl bg-muted" />}
                {policy.isError && <div className="rounded-2xl border border-destructive/30 p-4 text-sm text-destructive">策略加载失败。</div>}
                {policy.data && <>
                    <div className="flex items-center justify-between rounded-2xl border bg-muted/30 p-4"><span className="text-sm text-muted-foreground">当前 revision</span><span className="font-mono text-sm font-semibold">{policy.data.active_revision}</span></div>
                    <div className="space-y-2"><label className="text-sm font-medium">策略模式</label><Select value={mode} onValueChange={(value) => setMode(value as PolicyMode)}><SelectTrigger className="w-full rounded-xl"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="inherit">继承上级</SelectItem><SelectItem value="auto">自动选择</SelectItem><SelectItem value="prefer">按顺序优先</SelectItem><SelectItem value="force">强制限定</SelectItem></SelectContent></Select></div>
                    {(mode === 'prefer' || mode === 'force') && <div className="space-y-2"><label className="text-sm font-medium">协议优先级</label><div className="space-y-2">{PROTOCOLS.map((protocol) => <button key={protocol} type="button" onClick={() => toggleProtocol(protocol)} className={`flex w-full items-center justify-between rounded-xl border p-3 text-sm transition-colors ${protocols.includes(protocol) ? 'border-primary bg-primary/5 text-primary' : 'hover:bg-muted/50'}`}><span>{protocol}</span><span className="text-xs">{protocols.includes(protocol) ? `优先级 ${protocols.indexOf(protocol) + 1}` : '未选择'}</span></button>)}</div><p className="text-xs text-muted-foreground">按点击加入的顺序确定优先级；取消后可重新加入调整顺序。</p></div>}
                    <Button disabled={mutation.isPending} onClick={save} className="h-11 w-full rounded-xl">{mutation.isPending ? '保存中…' : '保存策略'}</Button>
                </>}
            </MorphingDialogDescription>
        </>
    );
}

export function ScopedPolicyDialog({ kind, id, name, compact = false }: { kind: 'groups' | 'group-presets'; id: number; name: string; compact?: boolean }) {
    return (
        <MorphingDialog>
            <MorphingDialogTrigger aria-label="协议策略" className={compact ? 'p-1 rounded-md hover:bg-muted text-muted-foreground hover:text-foreground' : 'p-1.5 rounded-lg transition-colors hover:bg-muted text-muted-foreground hover:text-foreground'}><Route className={compact ? 'size-3.5' : 'size-4'} /></MorphingDialogTrigger>
            <MorphingDialogContainer><MorphingDialogContent className="w-[calc(100vw-2rem)] max-w-lg rounded-3xl bg-card p-6 text-card-foreground"><ScopedPolicyContent kind={kind} id={id} name={name} /></MorphingDialogContent></MorphingDialogContainer>
        </MorphingDialog>
    );
}
