"use client";

/**
 * RequestErrorChart — dual-axis ComposedChart combining:
 *   - Area: request rate (req/min, left axis)
 *   - Line: HTTP error rate % (right axis)
 *
 * Replaces the two separate MetricAreaChart instances for HTTP traffic,
 * reducing visual noise while keeping all information.
 */

import {
  ComposedChart,
  Area,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from "recharts";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";
import type { TimePoint } from "@/lib/api/telemetry/prometheus";

interface RequestErrorChartProps {
  requestRateSeries: TimePoint[];
  errorRateSeries: TimePoint[];
}

function formatTime(unixSec: number): string {
  return new Date(unixSec * 1000).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function RequestErrorChart({
  requestRateSeries,
  errorRateSeries,
}: RequestErrorChartProps) {
  // Merge two series on timestamp — use requestRate as the primary key
  const merged = requestRateSeries.map((rp) => {
    const ep = errorRateSeries.find((e) => e.t === rp.t);
    return {
      t: rp.t,
      label: formatTime(rp.t),
      req: rp.v,
      err: ep?.v ?? 0,
    };
  });

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">HTTP traffic</CardTitle>
        <CardDescription className="text-xs">
          Request rate + error rate · last 30 min
        </CardDescription>
      </CardHeader>
      <CardContent className="pb-3">
        <ResponsiveContainer width="100%" height={180}>
          <ComposedChart data={merged} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
            <defs>
              <linearGradient id="reqGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%"  stopColor="var(--chart-2)" stopOpacity={0.25} />
                <stop offset="95%" stopColor="var(--chart-2)" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid
              strokeDasharray="3 3"
              stroke="var(--border)"
              opacity={0.4}
              vertical={false}
            />
            <XAxis
              dataKey="label"
              tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
              axisLine={false}
              tickLine={false}
              interval="preserveStartEnd"
            />
            {/* Left axis — requests/min */}
            <YAxis
              yAxisId="req"
              tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
              axisLine={false}
              tickLine={false}
              allowDecimals={false}
            />
            {/* Right axis — error % */}
            <YAxis
              yAxisId="err"
              orientation="right"
              tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
              axisLine={false}
              tickLine={false}
              tickFormatter={(v) => `${v}%`}
            />
            <Tooltip
              contentStyle={{
                background: "var(--popover)",
                border: "1px solid var(--border)",
                borderRadius: "8px",
                fontSize: "12px",
                color: "var(--popover-foreground)",
              }}
              formatter={(value: number, name: string) =>
                name === "req"
                  ? [`${Math.round(value)} req/min`, "Requests"]
                  : [`${value.toFixed(2)}%`, "Error rate"]
              }
              cursor={{ stroke: "var(--border)", strokeWidth: 1 }}
            />
            <Legend
              iconType="circle"
              iconSize={8}
              wrapperStyle={{ fontSize: 11, color: "var(--muted-foreground)" }}
              formatter={(value) =>
                value === "req" ? "req/min" : "error %"
              }
            />
            <Area
              yAxisId="req"
              type="monotone"
              dataKey="req"
              stroke="var(--chart-2)"
              strokeWidth={1.5}
              fill="url(#reqGradient)"
              dot={false}
              activeDot={{ r: 3 }}
              isAnimationActive={false}
            />
            <Line
              yAxisId="err"
              type="monotone"
              dataKey="err"
              stroke="var(--chart-4)"
              strokeWidth={1.5}
              dot={false}
              activeDot={{ r: 3 }}
              isAnimationActive={false}
            />
          </ComposedChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  );
}
