export interface ExistingGroupMember {
    id?: number;
    channel_id: number;
    model_name: string;
    priority: number;
    weight: number;
}

export interface EditedGroupMember {
    item_id?: number;
    channel_id: number;
    name: string;
    weight?: number;
}

export interface GroupMemberChanges {
    items_to_add: Array<{
        channel_id: number;
        model_name: string;
        priority: number;
        weight: number;
    }>;
    items_to_update: Array<{
        id: number;
        priority: number;
        weight: number;
    }>;
    items_to_delete: number[];
}

export function buildGroupMemberChanges(
    currentItems: ExistingGroupMember[],
    editedMembers: EditedGroupMember[],
): GroupMemberChanges {
    const originalItems = [...currentItems].sort((a, b) => a.priority - b.priority);
    const originalById = new Map<number, ExistingGroupMember>();
    originalItems.forEach((item) => {
        if (typeof item.id === 'number') originalById.set(item.id, item);
    });

    const retainedIds = new Set<number>();
    editedMembers.forEach((member) => {
        if (typeof member.item_id === 'number') retainedIds.add(member.item_id);
    });

    const items_to_delete = [...originalById.keys()].filter((id) => !retainedIds.has(id));
    const items_to_add = editedMembers
        .map((member, index) => ({ member, priority: index + 1 }))
        .filter(({ member }) => typeof member.item_id !== 'number')
        .filter(({ member }) => !originalItems.some((existing) => (
            existing.channel_id === member.channel_id
            && existing.model_name === member.name
            && typeof existing.id === 'number'
            && retainedIds.has(existing.id)
        )))
        .map(({ member, priority }) => ({
            channel_id: member.channel_id,
            model_name: member.name,
            priority,
            weight: member.weight ?? 1,
        }));

    const items_to_update = editedMembers
        .map((member, index) => ({ member, priority: index + 1 }))
        .filter(({ member }) => typeof member.item_id === 'number')
        .flatMap(({ member, priority }) => {
            const id = member.item_id as number;
            const original = originalById.get(id);
            const weight = member.weight ?? 1;
            if (!original || (original.priority === priority && original.weight === weight)) return [];
            return [{ id, priority, weight }];
        });

    return { items_to_add, items_to_update, items_to_delete };
}
