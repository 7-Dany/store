import { memo } from "react";

interface StatTileProps {
  label: string;
  value: string;
}

export const StatTile = memo(function StatTile({
  label,
  value,
}: StatTileProps) {
  return (
    <div className="rounded-xl border border-border/70 bg-muted/30 px-3 py-2.5">
      <p className="text-[10px] uppercase tracking-[0.14em] text-muted-foreground/60">
        {label}
      </p>
      <p className="mt-1 font-mono text-xs font-semibold text-foreground">
        {value}
      </p>
    </div>
  );
});
