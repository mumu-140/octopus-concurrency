'use client';

import { useEffect, useMemo, useState } from 'react';
import { ArrowLeft, Plus, Trash2 } from 'lucide-react';
import type { ApiError } from '@/api/types';
import {
    useChannelProtocolPolicy,
    useReplaceChannelProtocolPolicy,
    type ModelProtocolOverridePolicy,
    type Protocol,
    type ProtocolProfilePolicy,
} from '@/api/endpoints/protocol-routing';
import { toast } from '@/components/common/Toast';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';

const PROTOCOLS: Protocol[] = ['openai_chat', 'openai_response', 'anthropic'];
const emptyProfile = (protocol: Protocol): ProtocolProfilePolicy => ({ protocol, enabled: true, base_urls: [], custom_headers: [] });

export function ChannelPolicyPanel({ channelId, channelKeys, onBack }: {
    channelId: number;
    channelKeys: Array<{ id: number; remark?: string }>;
    onBack: () => void;
}) {
    const query = useChannelProtocolPolicy(channelId);
    const mutation = useReplaceChannelProtocolPolicy(channelId);
    const [profiles, setProfiles] = useState<ProtocolProfilePolicy[]>([]);
    const [overrides, setOverrides] = useState<ModelProtocolOverridePolicy[]>([]);

    useEffect(() => {
        if (!query.data) return;
        queueMicrotask(() => {
            setProfiles(query.data!.policy.profiles);
            setOverrides(query.data!.policy.overrides);
        });
    }, [query.data]);

    const missingProtocols = useMemo(() => PROTOCOLS.filter((protocol) => !profiles.some((item) => item.protocol === protocol)), [profiles]);
    const save = () => {
        if (!query.data) return;
        for (const profile of profiles) {
            if (profile.base_urls.some((item) => !item.url.trim())) {
                toast.error('Profile 地址不能为空');
                return;
            }
            if (profile.custom_headers.some((item) => !item.header_key.trim() || !item.header_value)) {
                toast.error('Profile Header 名和值不能为空');
                return;
            }
            if (profile.param_override?.trim()) {
                try { JSON.parse(profile.param_override); } catch { toast.error(`${profile.protocol} 参数覆盖不是有效 JSON`); return; }
            }
        }
        if (overrides.some((item) => !item.upstream_model.trim())) {
            toast.error('模型 Override 必须填写上游模型名');
            return;
        }
        if (overrides.some((item) => item.preferred_protocols.length === 0)) {
            toast.error('模型 Override 至少选择一个协议');
            return;
        }
        mutation.mutate(
            { expected_revision: query.data.active_revision, profiles, overrides },
            {
                onSuccess: () => { toast.success('渠道协议策略已保存'); onBack(); },
                onError: (error) => {
                    const apiError = error as unknown as ApiError;
                    if (apiError.code === 409) {
                        toast.error('配置已更新，请刷新后重试');
                        query.refetch();
                    } else toast.error('保存失败', { description: apiError.message });
                },
            },
        );
    };

    if (query.isLoading) return <div className="h-64 animate-pulse rounded-2xl bg-muted" />;
    if (query.isError || !query.data) return <div className="space-y-3"><p className="rounded-2xl border border-destructive/30 p-4 text-destructive">渠道协议策略加载失败。</p><Button onClick={() => query.refetch()}>重试</Button></div>;

    return (
        <div className="space-y-5">
            <div className="flex items-center justify-between gap-3">
                <Button variant="ghost" onClick={onBack} className="rounded-xl"><ArrowLeft className="size-4" />返回</Button>
                <span className="text-xs text-muted-foreground">revision {query.data.active_revision}</span>
            </div>
            <section className="space-y-3">
                <div className="flex items-center justify-between"><h3 className="font-semibold">协议 Profile</h3>{missingProtocols.length > 0 && <Select onValueChange={(value) => setProfiles((items) => [...items, emptyProfile(value as Protocol)])}><SelectTrigger className="w-36 rounded-xl"><SelectValue placeholder="新增协议" /></SelectTrigger><SelectContent>{missingProtocols.map((protocol) => <SelectItem key={protocol} value={protocol}>{protocol}</SelectItem>)}</SelectContent></Select>}</div>
                {profiles.length === 0 && <p className="rounded-2xl border border-dashed p-5 text-center text-sm text-muted-foreground">未配置 Profile，渠道将使用原始配置。</p>}
                {profiles.map((profile, index) => (
                    <div key={profile.protocol} className="space-y-3 rounded-2xl border p-4">
                        <div className="flex items-center justify-between"><strong className="text-sm">{profile.protocol}</strong><div className="flex items-center gap-3"><Switch checked={profile.enabled} onCheckedChange={(enabled) => setProfiles((items) => items.map((item, i) => i === index ? { ...item, enabled } : item))} /><Button size="icon" variant="ghost" onClick={() => setProfiles((items) => items.filter((_, i) => i !== index))}><Trash2 className="size-4" /></Button></div></div>
                        <div className="space-y-2">
                            {profile.base_urls.map((baseUrl, urlIndex) => <div key={urlIndex} className="flex gap-2"><Input value={baseUrl.url} onChange={(event) => setProfiles((items) => items.map((item, i) => i === index ? { ...item, base_urls: item.base_urls.map((url, j) => j === urlIndex ? { ...url, url: event.target.value } : url) } : item))} placeholder="协议专用 Base URL" className="rounded-xl" /><Button size="icon" variant="ghost" onClick={() => setProfiles((items) => items.map((item, i) => i === index ? { ...item, base_urls: item.base_urls.filter((_, j) => j !== urlIndex) } : item))}><Trash2 className="size-4" /></Button></div>)}
                            <Button size="sm" variant="outline" className="rounded-xl" onClick={() => setProfiles((items) => items.map((item, i) => i === index ? { ...item, base_urls: [...item.base_urls, { url: '', delay: 0 }] } : item))}><Plus className="size-4" />Base URL</Button>
                        </div>
                        <div className="space-y-2">
                            {profile.custom_headers.map((header, headerIndex) => <div key={headerIndex} className="grid grid-cols-[1fr_1fr_auto] gap-2"><Input value={header.header_key} onChange={(event) => setProfiles((items) => items.map((item, i) => i === index ? { ...item, custom_headers: item.custom_headers.map((value, j) => j === headerIndex ? { ...value, header_key: event.target.value } : value) } : item))} placeholder="Header 名" className="rounded-xl" /><Input value={header.header_value} onChange={(event) => setProfiles((items) => items.map((item, i) => i === index ? { ...item, custom_headers: item.custom_headers.map((value, j) => j === headerIndex ? { ...value, header_value: event.target.value } : value) } : item))} placeholder="Header 值" className="rounded-xl" /><Button size="icon" variant="ghost" onClick={() => setProfiles((items) => items.map((item, i) => i === index ? { ...item, custom_headers: item.custom_headers.filter((_, j) => j !== headerIndex) } : item))}><Trash2 className="size-4" /></Button></div>)}
                            <Button size="sm" variant="outline" className="rounded-xl" onClick={() => setProfiles((items) => items.map((item, i) => i === index ? { ...item, custom_headers: [...item.custom_headers, { header_key: '', header_value: '' }] } : item))}><Plus className="size-4" />Header</Button>
                        </div>
                        <textarea value={profile.param_override ?? ''} onChange={(event) => setProfiles((items) => items.map((item, i) => i === index ? { ...item, param_override: event.target.value } : item))} placeholder={'高级参数覆盖 JSON，例如 {"temperature":0.2}'} className="min-h-20 w-full rounded-xl border bg-transparent p-3 font-mono text-xs outline-none focus:ring-2 focus:ring-ring" />
                    </div>
                ))}
            </section>
            <section className="space-y-3">
                <div className="flex items-center justify-between"><h3 className="font-semibold">模型 Override</h3><Button variant="outline" size="sm" className="rounded-xl" onClick={() => setOverrides((items) => [...items, { channel_key_id: 0, upstream_model: '', mode: 'prefer', preferred_protocols: ['openai_chat'], enabled: true }])}><Plus className="size-4" />新增</Button></div>
                {overrides.length === 0 && <p className="rounded-2xl border border-dashed p-5 text-center text-sm text-muted-foreground">未配置模型级覆盖。</p>}
                {overrides.map((override, index) => (
                    <div key={`${index}-${override.upstream_model}`} className="grid gap-3 rounded-2xl border p-4 sm:grid-cols-2">
                        <Input value={override.upstream_model} onChange={(event) => setOverrides((items) => items.map((item, i) => i === index ? { ...item, upstream_model: event.target.value } : item))} placeholder="上游模型名" className="rounded-xl" />
                        <Select value={String(override.channel_key_id)} onValueChange={(value) => setOverrides((items) => items.map((item, i) => i === index ? { ...item, channel_key_id: Number(value) } : item))}><SelectTrigger className="w-full rounded-xl"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="0">全部 Key</SelectItem>{channelKeys.map((key) => <SelectItem key={key.id} value={String(key.id)}>Key {key.id}{key.remark ? ` · ${key.remark}` : ''}</SelectItem>)}</SelectContent></Select>
                        <Select value={override.mode} onValueChange={(value) => setOverrides((items) => items.map((item, i) => i === index ? { ...item, mode: value as ModelProtocolOverridePolicy['mode'] } : item))}><SelectTrigger className="w-full rounded-xl"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="prefer">优先</SelectItem><SelectItem value="force">强制</SelectItem></SelectContent></Select>
                        <div className="flex flex-wrap gap-1 rounded-xl border p-2 sm:col-span-2">{PROTOCOLS.map((protocol) => { const selected = override.preferred_protocols.includes(protocol); return <button key={protocol} type="button" onClick={() => setOverrides((items) => items.map((item, i) => i === index ? { ...item, preferred_protocols: selected ? item.preferred_protocols.filter((value) => value !== protocol) : [...item.preferred_protocols, protocol] } : item))} className={`rounded-lg px-2 py-1 text-xs ${selected ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>{protocol}{selected ? ` · ${override.preferred_protocols.indexOf(protocol) + 1}` : ''}</button>; })}</div>
                        <div className="flex items-center gap-2 text-sm"><Switch checked={override.enabled} onCheckedChange={(enabled) => setOverrides((items) => items.map((item, i) => i === index ? { ...item, enabled } : item))} />启用</div>
                        <Button variant="ghost" className="justify-self-end text-destructive" onClick={() => setOverrides((items) => items.filter((_, i) => i !== index))}><Trash2 className="size-4" />删除</Button>
                    </div>
                ))}
            </section>
            <Button className="w-full rounded-2xl h-12" disabled={mutation.isPending} onClick={save}>{mutation.isPending ? '保存中…' : '保存渠道协议策略'}</Button>
        </div>
    );
}
