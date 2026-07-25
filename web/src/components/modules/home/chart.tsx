'use client';

import {
    useStatsDaily,
    useStatsHourly,
    useStatsTotal,
} from '@/api/endpoints/stats';
import { Tabs, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import {
    buildStatsChartSummary,
    type Formatted,
    type HeroValue,
    type MetricsRow,
} from '@/components/modules/home/chart-summary';
import { useHomeViewStore, type ChartPeriod } from '@/components/modules/home/store';
import { formatMoneyLabel, type ChartPoint } from '@/components/modules/home/stats-window';
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import { useMemo } from 'react';
import { useTranslations } from 'next-intl';
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts';

const PERIOD_KEY: Record<ChartPeriod, 'today' | 'last7Days' | 'last30Days' | 'allTime'> = {
    '1': 'today',
    '7': 'last7Days',
    '30': 'last30Days',
    all: 'allTime',
};

type Translate = (key: string) => string;

export function StatsChart() {
    const t = useTranslations('home.summary');
    const total = useStatsTotal();
    const daily = useStatsDaily();
    const hourly = useStatsHourly();
    const period = useHomeViewStore((state) => state.chartPeriod);
    const setChartPeriod = useHomeViewStore((state) => state.setChartPeriod);

    const sortedDaily = useMemo(
        () => [...(daily.data ?? [])].sort((left, right) => left.date.localeCompare(right.date)),
        [daily.data],
    );
    const summary = useMemo(
        () => buildStatsChartSummary(period, total.data, hourly.data, sortedDaily),
        [period, total.data, hourly.data, sortedDaily],
    );
    const statsError = total.isError || daily.isError || hourly.isError;
    const statsLoading = total.isLoading || daily.isLoading || hourly.isLoading;

    return (
        <section
            className="rounded-3xl bg-card border-card-border border text-card-foreground custom-shadow"
            aria-busy={statsLoading}
        >
            {statsError && <StatsLoadError message={t('loadError')} />}
            <StatsChartHeader
                hero={summary.hero}
                period={period}
                setPeriod={setChartPeriod}
                t={t}
            />
            <StatsMetricsRow metrics={summary.metrics} t={t} />
            <CostAreaChart data={summary.chartData} label={t('headline.allTime')} />
        </section>
    );
}

function StatsLoadError({ message }: { message: string }) {
    return (
        <div className="mx-5 mt-4 rounded-xl bg-destructive/10 px-3 py-2 text-xs text-destructive" role="status">
            {message}
        </div>
    );
}

function heroUnitSuffix(unit: string): string {
    if (!unit || unit === '$') {
        return '';
    }
    return unit.replace(/\$$/, '');
}

function StatsChartHeader({
    hero,
    period,
    setPeriod,
    t,
}: {
    hero: HeroValue;
    period: ChartPeriod;
    setPeriod: (period: ChartPeriod) => void;
    t: Translate;
}) {
    const suffix = heroUnitSuffix(hero.unit);
    return (
        <header className="px-5 pt-5 pb-4 flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
            <div>
                <p className="text-xs text-muted-foreground">{t(`headline.${PERIOD_KEY[period]}`)}</p>
                <p className="mt-1 text-4xl md:text-5xl font-semibold tabular-nums tracking-tight">
                    {hero.value === undefined ? (
                        <span className="text-muted-foreground">—</span>
                    ) : (
                        <>
                            <span className="text-muted-foreground text-2xl mr-1">$</span>
                            <AnimatedNumber value={hero.value} />
                            {suffix && <span className="ml-1 text-xl text-muted-foreground">{suffix}</span>}
                        </>
                    )}
                </p>
            </div>
            <div className="max-w-full overflow-x-auto pb-0.5">
                <Tabs value={period} onValueChange={(value) => setPeriod(value as ChartPeriod)}>
                    <TabsList aria-label={t('periodLabel')}>
                        <TabsTrigger value="1">{t('periods.today')}</TabsTrigger>
                        <TabsTrigger value="7">{t('periods.last7Days')}</TabsTrigger>
                        <TabsTrigger value="30">{t('periods.last30Days')}</TabsTrigger>
                        <TabsTrigger value="all">{t('periods.allTime')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
        </header>
    );
}

function StatsMetricsRow({ metrics, t }: { metrics: MetricsRow; t: Translate }) {
    return (
        <div className="mx-5 grid grid-cols-3 gap-2 border-t border-border/60 py-3 text-sm tabular-nums sm:flex sm:items-baseline sm:gap-6">
            <StatItem label={t('metrics.requests')} value={metrics.requests} />
            <span className="hidden h-4 w-px bg-border/60 sm:block" />
            <StatItem label={t('metrics.tokens')} value={metrics.tokens} />
            <span className="hidden h-4 w-px bg-border/60 sm:block" />
            <StatItem label={t('metrics.waitTime')} value={metrics.waitTime} />
        </div>
    );
}

function CostAreaChart({ data, label }: { data: ChartPoint[]; label: string }) {
    const config = { total_cost: { label } };
    return (
        <ChartContainer config={config} className="h-40 w-full">
            <AreaChart accessibilityLayer data={data}>
                <defs>
                    <linearGradient id="fillCost" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="var(--chart-1)" stopOpacity={0.35} />
                        <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0.05} />
                    </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="date" tickLine={false} axisLine={false} />
                <YAxis tickLine={false} axisLine={false} tickFormatter={formatMoneyLabel} />
                <ChartTooltip cursor={false} content={<ChartTooltipContent indicator="line" />} />
                <Area
                    type="monotone"
                    dataKey="total_cost"
                    stroke="var(--chart-1)"
                    fill="url(#fillCost)"
                />
            </AreaChart>
        </ChartContainer>
    );
}

function StatItem({ label, value }: { label: string; value: Formatted | undefined }) {
    return (
        <div className="flex min-w-0 flex-col gap-0.5 sm:flex-row sm:items-baseline sm:gap-1.5">
            <span className="truncate text-xs text-muted-foreground">{label}</span>
            <span className="truncate font-medium">
                {value ? (
                    <>
                        <AnimatedNumber value={value.value} />
                        {value.unit && (
                            <span className="ml-0.5 text-xs text-muted-foreground">{value.unit}</span>
                        )}
                    </>
                ) : (
                    <span className="text-muted-foreground">—</span>
                )}
            </span>
        </div>
    );
}
