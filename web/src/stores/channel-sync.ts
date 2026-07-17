import { create } from 'zustand';
import type { ChannelSyncReport } from '@/api/endpoints/channel';

type ChannelSyncState = {
    lastReport: ChannelSyncReport | null;
    setLastReport: (report: ChannelSyncReport | null) => void;
};

// This report is session-scoped: it reflects the latest explicit sync from the
// current operator and must not masquerade as durable history.
export const useChannelSyncStore = create<ChannelSyncState>((set) => ({
    lastReport: null,
    setLastReport: (report) => set({ lastReport: report }),
}));
