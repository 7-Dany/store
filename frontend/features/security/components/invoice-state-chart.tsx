"use client";

/**
 * InvoiceStateChart — compact bar chart showing Bitcoin invoice state counts.
 *
 * Three bars: Pending / Confirmed / Expired. Height 140px.
 * Only rendered when Bitcoin is enabled (zmqConnected !== null in parent).
 */

import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  Cell,
  ResponsiveContainer,
} from "recharts";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";

interface InvoiceStateChartProps {
  pending: number;
  confirmed: number;
  expired: number;
}

const BARS = [
  { key: "Pending",   color: "var(--chart-1)" },
  { key: "Confirmed", color: "var(--chart-2)" },
  { key: "Expired",   color: "var(--chart-4)" },
] as const;

export function InvoiceStateChart({
  pending,
  confirmed,
  expired,
}: InvoiceStateChartProps) {
  const data = [
    { name: "Pending",   value: pending },
    { name: "Confirmed", value: confirmed },
    { name: "Expired",   value: expired },
  ];

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">Invoice states</CardTitle>
        <CardDescription className="text-xs">Current counts</CardDescription>
      </CardHeader>
      <CardContent className="pb-3">
        <ResponsiveContainer width="100%" height={140}>
          <BarChart data={data} margin={{ top: 4, right: 4, left: -16, bottom: 0 }}>
            <XAxis
              dataKey="name"
              tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
              axisLine={false}
              tickLine={false}
            />
            <YAxis
              tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
              axisLine={false}
              tickLine={false}
              allowDecimals={false}
            />
            <Tooltip
              contentStyle={{
                background: "var(--popover)",
                border: "1px solid var(--border)",
                borderRadius: "8px",
                fontSize: "12px",
                color: "var(--popover-foreground)",
              }}
              cursor={{ fill: "var(--muted)", opacity: 0.5 }}
            />
            <Bar dataKey="value" radius={[4, 4, 0, 0]}>
              {data.map((_, i) => (
                <Cell key={i} fill={BARS[i].color} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  );
}
