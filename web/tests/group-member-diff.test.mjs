import assert from 'node:assert/strict';
import test from 'node:test';
import { buildGroupMemberChanges } from '../src/components/modules/group/groupMemberDiff.ts';

test('re-adding a cleared member sends both delete and add', () => {
    const current = [
        { id: 11, channel_id: 7, model_name: 'deepseek-v4-pro', priority: 1, weight: 1 },
    ];
    const edited = [
        { channel_id: 7, name: 'deepseek-v4-pro', weight: 1 },
    ];

    assert.deepEqual(buildGroupMemberChanges(current, edited), {
        items_to_add: [
            { channel_id: 7, model_name: 'deepseek-v4-pro', priority: 1, weight: 1 },
        ],
        items_to_update: [],
        items_to_delete: [11],
    });
});

test('retained members are not duplicated and changed order is updated', () => {
    const current = [
        { id: 11, channel_id: 7, model_name: 'deepseek-v4-pro', priority: 1, weight: 1 },
        { id: 12, channel_id: 8, model_name: 'deepseek-v4-pro', priority: 2, weight: 1 },
    ];
    const edited = [
        { item_id: 12, channel_id: 8, name: 'deepseek-v4-pro', weight: 2 },
        { channel_id: 9, name: 'deepseek-v4-pro', weight: 1 },
    ];

    assert.deepEqual(buildGroupMemberChanges(current, edited), {
        items_to_add: [
            { channel_id: 9, model_name: 'deepseek-v4-pro', priority: 2, weight: 1 },
        ],
        items_to_update: [
            { id: 12, priority: 1, weight: 2 },
        ],
        items_to_delete: [11],
    });
});
