"use client";

import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";
import type { TimePoint } from "@/lib/api/telemetry/prometheus";

interface MetricAreaChartProps {
  title: string;
  description: string;
  data: TimePoint[];
  /** Label shown in tooltip and legend */
  seriesLabel: string;
  /** CSS variable key for the series color — e.g. "requests", "errors", "loginFails" */
  colorKey: string;
  /** CSS var for the color — e.g. "var(--chart-1)" */
  color: string;
  /** Unit suffix appended in the tooltip value — e.g. "/min", "%" */
  unit?: string;
  /** y-axis domain — defaults to ["auto","auto"] */
  yDomain?: [number | string, number | string];
  className?: string;
}

export function MetricAreaChart({
  title,
  description,
  data,
  seriesLabel,
  colorKey,
  color,
  unit = "",
  yDomain = ["auto", "auto"],
  className,
}: MetricAreaChartProps) {
  // Recharts needs named keys — transform TimePoint into { time, value }
  const chartData = data.map((p) => ({
    time: new Date(p.t * 1000).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    }),
    [colorKey]: Math.round(p.v * 100) / 100,
  }));

  // When all values are 0 (or no data), yDomain=[0,"auto"] collapses to [0,0]
  // and the chart area becomes invisible. Ensure the y-axis always has a
  // visible range by using at least 1 as the upper bound.
  const maxValue = chartData.length > 0
    ? Math.max(...chartData.map((d) => d[colorKey] as number))
    : 0;
  const effectiveDomain: [number | string, number | string] =
    yDomain[0] === 0 || yDomain[0] === "auto"
      ? [yDomain[0], maxValue > 0 ? yDomain[1] : 1]
      : yDomain;

  const chartConfig = {
    [colorKey]: {
      label: seriesLabel,
      color,
    },
  } satisfies ChartConfig;

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        <ChartContainer config={chartConfig} className="h-[160px] w-full">
          <AreaChart
            accessibilityLayer
            data={chartData}
            margin={{ top: 4, right: 4, left: 4, bottom: 0 }}
          >
            <defs>
              <linearGradient id={`grad-${colorKey}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={`var(--color-${colorKey})`} stopOpacity={0.3} />
                <stop offset="95%" stopColor={`var(--color-${colorKey})`} stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} strokeDasharray="3 3" className="stroke-border" />
            <XAxis
              dataKey="time"
              tickLine={false}
              axisLine={false}
              tick={{ fontSize: 10 }}
              tickMargin={6}
              interval="preserveStartEnd"
            />
            <YAxis
              domain={effectiveDomain}
              tickLine={false}
              axisLine={false}
              tick={{ fontSize: 10 }}
              tickMargin={4}
              width={40}
              tickFormatter={(v) => `${v}${unit}`}
            />
            <ChartTooltip
              content={
                <ChartTooltipContent
                  formatter={(value) => [`${value}${unit}`, seriesLabel]}
                  hideLabel
                  indicator="line"
                />
              }
            />
            <Area
              type="monotone"
              dataKey={colorKey}
              stroke={`var(--color-${colorKey})`}
              strokeWidth={1.5}
              fill={`url(#grad-${colorKey})`}
              dot={false}
              activeDot={{ r: 3 }}
            />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  );
}
