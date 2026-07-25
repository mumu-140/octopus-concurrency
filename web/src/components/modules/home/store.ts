'use client';

import { create } from 'zustand';
import { createJSONStorage, persist } from 'zustand/middleware';

export type RankSortMode = 'cost' | 'count' | 'tokens' | 'failed';
export type RankDimension = 'model' | 'channel' | 'group';
export type ChartPeriod = '1' | '7' | '30' | 'all';

interface HomeViewState {
    rankSortMode: RankSortMode;
    rankDimension: RankDimension;
    chartPeriod: ChartPeriod;
    setRankSortMode: (value: RankSortMode) => void;
    setRankDimension: (value: RankDimension) => void;
    setChartPeriod: (value: ChartPeriod) => void;
}

export const useHomeViewStore = create<HomeViewState>()(
    persist(
        (set) => ({
            rankSortMode: 'cost',
            rankDimension: 'channel',
            chartPeriod: '7',
            setRankSortMode: (value) => set({ rankSortMode: value }),
            setRankDimension: (value) => set({ rankDimension: value }),
            setChartPeriod: (value) => set({ chartPeriod: value }),
        }),
        {
            name: 'home-view-options-storage',
            storage: createJSONStorage(() => localStorage),
            partialize: (state) => ({
                rankSortMode: state.rankSortMode,
                rankDimension: state.rankDimension,
                chartPeriod: state.chartPeriod,
            }),
        }
    )
);
