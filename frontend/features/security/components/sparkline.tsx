import type { TimePoint } from "@/lib/api/telemetry/prometheus";

interface SparklineProps {
  data: TimePoint[];
  /** Which field to plot — defaults to "v" */
  width?: number;
  height?: number;
  /** Tailwind stroke colour class — e.g. "stroke-primary" */
  strokeClass?: string;
  /** Tailwind fill colour class for the gradient area */
  fillClass?: string;
  className?: string;
}

/**
 * Zero-dependency pure-SVG sparkline.
 * Stateless server-renderable component — no "use client" needed.
 * Renders an empty flat line when data is empty.
 */
export function Sparkline({
  data,
  width = 120,
  height = 32,
  strokeClass = "stroke-primary",
  fillClass = "fill-primary/10",
  className,
}: SparklineProps) {
  if (data.length < 2) {
    return (
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        aria-hidden="true"
        className={className}
      >
        <line
          x1="0"
          y1={height / 2}
          x2={width}
          y2={height / 2}
          className="stroke-muted-foreground/20"
          strokeWidth="1"
        />
      </svg>
    );
  }

  const values = data.map((p) => p.v);
  const minV = Math.min(...values);
  const maxV = Math.max(...values);
  const range = maxV - minV || 1;

  const pad = 2;
  const usableW = width - pad * 2;
  const usableH = height - pad * 2;

  const points = data.map((p, i) => {
    const x = pad + (i / (data.length - 1)) * usableW;
    const y = pad + (1 - (p.v - minV) / range) * usableH;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });

  const linePath = `M ${points.join(" L ")}`;

  // Close area path back along the bottom
  const firstX = (pad).toFixed(1);
  const lastX = (pad + usableW).toFixed(1);
  const bottomY = (pad + usableH).toFixed(1);
  const areaPath = `${linePath} L ${lastX},${bottomY} L ${firstX},${bottomY} Z`;

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      aria-hidden="true"
      className={className}
    >
      {/* Area fill */}
      <path d={areaPath} className={fillClass} strokeWidth="0" />
      {/* Line */}
      <path
        d={linePath}
        fill="none"
        className={strokeClass}
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
