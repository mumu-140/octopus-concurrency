'use client';

import { useMemo, useState, type ReactNode } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { useBatchUpdateChannels, type CustomHeader } from '@/api/endpoints/channel';
import { toast } from '@/components/common/Toast';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { UA_PRESETS } from './ua-presets';

type Props = { ids: number[]; open: boolean; onOpenChange: (open: boolean) => void; onDone: () => void };

export function BatchEditDialog({ ids, open, onOpenChange, onDone }: Props) {
    const mutation = useBatchUpdateChannels();
    const [applyHeaders, setApplyHeaders] = useState(false);
    const [headerMode, setHeaderMode] = useState<'merge' | 'replace'>('merge');
    const [headers, setHeaders] = useState<CustomHeader[]>([]);
    const [deletes, setDeletes] = useState('');
    const [applyConcurrency, setApplyConcurrency] = useState(false);
    const [maxConcurrency, setMaxConcurrency] = useState(3);
    const [applyRPM, setApplyRPM] = useState(false);
    const [maxRPM, setMaxRPM] = useState(0);
    const [applyAutoSync, setApplyAutoSync] = useState(false);
    const [autoSync, setAutoSync] = useState(false);
    const [refreshModels, setRefreshModels] = useState(false);
    const canSubmit = useMemo(() => ids.length > 0 && (applyHeaders || applyConcurrency || applyRPM || applyAutoSync || refreshModels),
        [ids.length, applyHeaders, applyConcurrency, applyRPM, applyAutoSync, refreshModels]);

    const applyPreset = (id: string) => {
        const preset = UA_PRESETS.find((item) => item.id === id);
        if (preset) setHeaders(preset.headers);
    };

    const submit = () => mutation.mutate({
        ids,
        ...(applyHeaders ? {
            header_mode: headerMode,
            header_upserts: headers.filter((item) => item.header_key.trim()),
            header_deletes: deletes.split(',').map((item) => item.trim()).filter(Boolean),
        } : {}),
        ...(applyConcurrency ? { max_concurrency: maxConcurrency } : {}),
        ...(applyRPM ? { max_rpm: maxRPM } : {}),
        ...(applyAutoSync ? { auto_sync: autoSync } : {}),
        refresh_models: refreshModels,
    }, {
        onSuccess: (result) => {
            const failed = Object.keys(result.errors ?? {}).length;
            if (failed) toast.warning(`已更新 ${result.updated} 个渠道，${failed} 个失败`);
            else toast.success(`已更新 ${result.updated} 个渠道`);
            onOpenChange(false);
            onDone();
        },
        onError: (error) => toast.error(error.message),
    });

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-2xl overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>批量编辑渠道</DialogTitle>
                    <DialogDescription>已选择 {ids.length} 个渠道。只有勾选的配置会被修改。</DialogDescription>
                </DialogHeader>
                <div className="space-y-5">
                    <BatchSection checked={applyHeaders} onCheckedChange={setApplyHeaders} title="请求 Header">
                        <div className="grid gap-3 sm:grid-cols-2">
                            <Select onValueChange={applyPreset} disabled={!applyHeaders}>
                                <SelectTrigger><SelectValue placeholder="选择 UA 预设" /></SelectTrigger>
                                <SelectContent>{UA_PRESETS.map((item) => <SelectItem key={item.id} value={item.id}>{item.label}</SelectItem>)}</SelectContent>
                            </Select>
                            <Select value={headerMode} onValueChange={(value) => setHeaderMode(value as 'merge' | 'replace')} disabled={!applyHeaders}>
                                <SelectTrigger><SelectValue /></SelectTrigger>
                                <SelectContent><SelectItem value="merge">合并 Header</SelectItem><SelectItem value="replace">替换全部 Header</SelectItem></SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            {headers.map((header, index) => (
                                <div key={index} className="flex gap-2">
                                    <Input disabled={!applyHeaders} value={header.header_key} placeholder="Header" onChange={(e) => setHeaders((items) => items.map((item, i) => i === index ? { ...item, header_key: e.target.value } : item))} />
                                    <Input disabled={!applyHeaders} value={header.header_value} placeholder="Value" onChange={(e) => setHeaders((items) => items.map((item, i) => i === index ? { ...item, header_value: e.target.value } : item))} />
                                    <Button type="button" variant="ghost" size="icon" disabled={!applyHeaders} onClick={() => setHeaders((items) => items.filter((_, i) => i !== index))}><Trash2 className="size-4" /></Button>
                                </div>
                            ))}
                            <Button type="button" variant="outline" size="sm" disabled={!applyHeaders} onClick={() => setHeaders((items) => [...items, { header_key: '', header_value: '' }])}><Plus className="mr-1 size-4" />添加 Header</Button>
                        </div>
                        <Input disabled={!applyHeaders} value={deletes} onChange={(e) => setDeletes(e.target.value)} placeholder="删除 Header，逗号分隔，例如 X-Test, originator" />
                    </BatchSection>

                    <div className="grid gap-4 sm:grid-cols-2">
                        <BatchNumber title="最大并发" checked={applyConcurrency} onCheckedChange={setApplyConcurrency} value={maxConcurrency} onChange={setMaxConcurrency} hint="0 表示不限" />
                        <BatchNumber title="每分钟请求数" checked={applyRPM} onCheckedChange={setApplyRPM} value={maxRPM} onChange={setMaxRPM} hint="0 表示不限" />
                    </div>

                    <BatchSection checked={applyAutoSync} onCheckedChange={setApplyAutoSync} title="自动刷新模型">
                        <div className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"><span>{autoSync ? '启用' : '禁用'}</span><Switch checked={autoSync} onCheckedChange={setAutoSync} disabled={!applyAutoSync} /></div>
                    </BatchSection>
                    <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={refreshModels} onChange={(e) => setRefreshModels(e.target.checked)} />立即刷新并保存所选渠道的模型列表</label>
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
                    <Button disabled={!canSubmit || mutation.isPending} onClick={submit}>{mutation.isPending ? '处理中...' : '应用批量配置'}</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

function BatchSection({ checked, onCheckedChange, title, children }: { checked: boolean; onCheckedChange: (value: boolean) => void; title: string; children: ReactNode }) {
    return <section className="space-y-3 rounded-md border p-4"><label className="flex items-center gap-2 font-medium"><input type="checkbox" checked={checked} onChange={(e) => onCheckedChange(e.target.checked)} />{title}</label>{children}</section>;
}

function BatchNumber({ title, checked, onCheckedChange, value, onChange, hint }: { title: string; checked: boolean; onCheckedChange: (value: boolean) => void; value: number; onChange: (value: number) => void; hint: string }) {
    return <BatchSection checked={checked} onCheckedChange={onCheckedChange} title={title}><Input type="number" min={0} step={1} disabled={!checked} value={value} onChange={(e) => onChange(Math.max(0, Number.parseInt(e.target.value || '0', 10) || 0))} /><p className="text-xs text-muted-foreground">{hint}</p></BatchSection>;
}
