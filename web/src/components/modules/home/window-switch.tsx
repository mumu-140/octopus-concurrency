'use client';

import { useTranslations } from 'next-intl';
import { Tabs, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import { useHomeViewStore, type ChartPeriod } from '@/components/modules/home/store';

/**
 * 共享时间窗口切换：同时驱动上方统计图与下方排行榜。
 * 绑定 store 中的 chartPeriod，作为全页面的时间维度开关。
 */
export function WindowSwitch() {
    const t = useTranslations('home.summary');
    const period = useHomeViewStore((state) => state.chartPeriod);
    const setChartPeriod = useHomeViewStore((state) => state.setChartPeriod);

    return (
        <Tabs value={period} onValueChange={(v) => setChartPeriod(v as ChartPeriod)}>
            <TabsList>
                <TabsTrigger value="1">{t('periods.today')}</TabsTrigger>
                <TabsTrigger value="7">{t('periods.last7Days')}</TabsTrigger>
                <TabsTrigger value="30">{t('periods.last30Days')}</TabsTrigger>
                <TabsTrigger value="all">{t('periods.allTime')}</TabsTrigger>
            </TabsList>
        </Tabs>
    );
}
