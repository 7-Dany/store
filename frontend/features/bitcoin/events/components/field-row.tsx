import { memo } from "react";
import { IconCheck, IconCopy } from "@tabler/icons-react";
import { cn } from "@/lib/utils";

interface FieldRowProps {
  label: string;
  value: string;
  copyKey?: string;
  copied: string | null;
  onCopy: (val: string, key: string) => void;
  accentClass?: string;
}

export const FieldRow = memo(function FieldRow({
  label,
  value,
  copyKey,
  copied,
  onCopy,
  accentClass,
}: FieldRowProps) {
  return (
    <div className="flex items-start gap-2.5 border-b border-border/50 py-2.5 last:border-b-0">
      {label && (
        <span className="w-16 shrink-0 pt-px text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground/60">
          {label}
        </span>
      )}
      <div className="flex min-w-0 flex-1 items-start justify-between gap-1.5">
        <p
          className={cn(
            "min-w-0 break-all font-mono text-[12px] leading-relaxed text-foreground",
            accentClass,
          )}
        >
          {value}
        </p>
        {copyKey && (
          <button
            type="button"
            onClick={() => onCopy(value, copyKey)}
            aria-label={copied === copyKey ? "Copied" : `Copy ${label || value}`}
            className="shrink-0 rounded p-1 text-muted-foreground/50 transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {copied === copyKey
              ? <IconCheck size={11} stroke={2} className="text-green-500" aria-hidden="true" />
              : <IconCopy size={11} stroke={1.8} aria-hidden="true" />}
          </button>
        )}
      </div>
    </div>
  );
});
