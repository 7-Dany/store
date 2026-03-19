import { cn } from "@/lib/utils";

interface StepDotsProps {
  total: number;
  current: number;
  className?: string;
}

export function StepDots({ total, current, className }: StepDotsProps) {
  return (
    <div className={cn("flex items-center justify-center gap-1.5", className)} aria-hidden>
      {Array.from({ length: total }, (_, i) => (
        <span
          key={i}
          className={cn(
            "block rounded-full transition-all duration-300",
            i === current
              ? "h-1.5 w-4 bg-primary"
              : i < current
                ? "size-1.5 bg-primary/40"
                : "size-1.5 bg-border",
          )}
        />
      ))}
    </div>
  );
}
