'use client';

import { create } from 'zustand';
import { createJSONStorage, persist } from 'zustand/middleware';

export type RankSortMode = 'cost' | 'count' | 'tokens' | 'failed';
export type RankGroupMode = 'channel' | 'group';
export type ChartPeriod = '1' | '7' | '30' | 'all';

interface HomeViewState {
    rankSortMode: RankSortMode;
    rankGroupMode: RankGroupMode;
    chartPeriod: ChartPeriod;
    setRankSortMode: (value: RankSortMode) => void;
    setRankGroupMode: (value: RankGroupMode) => void;
    setChartPeriod: (value: ChartPeriod) => void;
}

export const useHomeViewStore = create<HomeViewState>()(
    persist(
        (set) => ({
            rankSortMode: 'cost',
            rankGroupMode: 'channel',
            chartPeriod: '7',
            setRankSortMode: (value) => set({ rankSortMode: value }),
            setRankGroupMode: (value) => set({ rankGroupMode: value }),
            setChartPeriod: (value) => set({ chartPeriod: value }),
        }),
        {
            name: 'home-view-options-storage',
            storage: createJSONStorage(() => localStorage),
            partialize: (state) => ({
                rankSortMode: state.rankSortMode,
                rankGroupMode: state.rankGroupMode,
                chartPeriod: state.chartPeriod,
            }),
        }
    )
);
