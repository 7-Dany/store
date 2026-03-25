"use client";

import { Bar, BarChart, CartesianGrid, XAxis, YAxis, Cell } from "recharts";
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
import {
  Empty,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { IconChartBar } from "@tabler/icons-react";
import type { ErrorBreakdown } from "@/lib/api/telemetry/prometheus";

interface ErrorBarChartProps {
  data: ErrorBreakdown[];
}

const chartConfig = {
  value: {
    label: "Errors",
    color: "var(--chart-4)",
  },
} satisfies ChartConfig;

// Assign progressively darker chart colors to bars by index
const BAR_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
];

export function ErrorBarChart({ data }: ErrorBarChartProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Errors by component</CardTitle>
        <CardDescription>
          app_errors_total — last hour, top 8 components
        </CardDescription>
      </CardHeader>
      <CardContent>
        {data.length === 0 ? (
          <Empty className="border-dashed py-8">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <IconChartBar />
              </EmptyMedia>
              <EmptyTitle className="text-base">
                No errors in the last hour
              </EmptyTitle>
            </EmptyHeader>
          </Empty>
        ) : (
          <ChartContainer config={chartConfig} className="h-50 w-full">
            <BarChart
              accessibilityLayer
              data={data}
              layout="vertical"
              margin={{ top: 0, right: 8, left: 0, bottom: 0 }}
            >
              <CartesianGrid
                horizontal={false}
                strokeDasharray="3 3"
                className="stroke-border"
              />
              <XAxis
                type="number"
                tickLine={false}
                axisLine={false}
                tick={{ fontSize: 10 }}
                tickMargin={4}
              />
              <YAxis
                type="category"
                dataKey="name"
                tickLine={false}
                axisLine={false}
                tick={{ fontSize: 10 }}
                tickMargin={4}
                width={72}
              />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    formatter={(value) => [`${value}`, "errors"]}
                    hideLabel
                    indicator="dot"
                  />
                }
              />
              <Bar dataKey="value" radius={[0, 4, 4, 0]}>
                {data.map((_, i) => (
                  <Cell key={i} fill={BAR_COLORS[i % BAR_COLORS.length]} />
                ))}
              </Bar>
            </BarChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}
