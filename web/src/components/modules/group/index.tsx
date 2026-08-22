'use client';

import { useMemo } from 'react';
import { GroupCard } from './Card';
import { useGroupList } from '@/api/endpoints/group';
import { useSearchStore, useToolbarViewOptionsStore } from '@/components/modules/toolbar';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';

// 分组卡目标宽度:旧 4 列 ~276px 的 1.5 倍,一行 3-4 个,不排挤 5 列
const GROUP_COLUMN_MIN_WIDTH = 414;
function resolveGroupColumns(width: number): number {
    const byMinWidth = Math.max(1, Math.floor((width + 16) / (GROUP_COLUMN_MIN_WIDTH + 16)));
    if (byMinWidth >= 4) return width >= 1420 ? 4 : 3;
    return byMinWidth;
}

export function Group() {
    const { data: groups } = useGroupList();
    const pageKey = 'group' as const;
    const searchTerm = useSearchStore((s) => s.getSearchTerm(pageKey));
    const sortField = useToolbarViewOptionsStore((s) => s.getSortField(pageKey));
    const sortOrder = useToolbarViewOptionsStore((s) => s.getSortOrder(pageKey));

    const sortedGroups = useMemo(() => {
        if (!groups) return [];
        return [...groups].sort((a, b) => {
            // 置顶优先：pinned 组排在前面，组内按 pinned_at desc
            if (!!a.pinned !== !!b.pinned) return a.pinned ? -1 : 1;
            if (a.pinned && b.pinned) {
                const ta = a.pinned_at ? new Date(a.pinned_at).getTime() : 0;
                const tb = b.pinned_at ? new Date(b.pinned_at).getTime() : 0;
                if (ta !== tb) return tb - ta;
            }
            const diff = sortField === 'name'
                ? a.name.localeCompare(b.name)
                : (a.id || 0) - (b.id || 0);
            return sortOrder === 'asc' ? diff : -diff;
        });
    }, [groups, sortField, sortOrder]);

    const visibleGroups = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        return !term ? sortedGroups : sortedGroups.filter((g) => g.name.toLowerCase().includes(term));
    }, [sortedGroups, searchTerm]);

    return (
        <VirtualizedGrid
            items={visibleGroups}
            columns={resolveGroupColumns}
            estimateItemHeight={520}
            getItemKey={(group, index) => group.id ?? `group-${index}`}
            renderItem={(group) => <GroupCard group={group} />}
        />
    );
}
