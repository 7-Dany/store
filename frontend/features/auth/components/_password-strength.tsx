import { cn } from "@/lib/utils";
import { IconCheck } from "@tabler/icons-react";
import {
  analyzePassword,
  PASSWORD_REQUIREMENTS,
  type StrengthLevel,
} from "@/lib/auth/password";

const LEVEL_COLOR: Record<StrengthLevel, string> = {
  empty: "bg-border",
  weak: "bg-destructive",
  fair: "bg-orange-400",
  good: "bg-yellow-400",
  strong: "bg-primary",
};

const LABEL_COLOR: Record<StrengthLevel, string> = {
  empty: "",
  weak: "text-destructive",
  fair: "text-orange-500 dark:text-orange-400",
  good: "text-yellow-600 dark:text-yellow-400",
  strong: "text-primary",
};

interface PasswordStrengthProps {
  value: string;
}

export function PasswordStrength({ value }: PasswordStrengthProps) {
  const { checks, score, level, label } = analyzePassword(value);

  if (!value) return null;

  return (
    <div className="mt-2.5 flex flex-col gap-2">
      {/* Strength bar */}
      <div className="flex gap-1">
        {[1, 2, 3, 4, 5].map((i) => (
          <div
            key={i}
            className={cn(
              "h-1 flex-1 rounded-full transition-all duration-300",
              score >= i ? LEVEL_COLOR[level] : "bg-border",
            )}
          />
        ))}
      </div>

      {label && (
        <p className="text-xs text-muted-foreground">
          Strength:{" "}
          <span className={cn("font-medium", LABEL_COLOR[level])}>{label}</span>
        </p>
      )}

      {/* Requirements checklist */}
      <div className="grid grid-cols-2 gap-x-3 gap-y-0.5">
        {PASSWORD_REQUIREMENTS.map(({ key, label: reqLabel }) => (
          <span
            key={key}
            className={cn(
              "flex items-center gap-1 text-xs transition-colors duration-200",
              checks[key] ? "text-primary" : "text-muted-foreground",
            )}
          >
            <IconCheck
              size={10}
              stroke={3}
              className={cn(
                "shrink-0 transition-opacity duration-200",
                checks[key] ? "opacity-100" : "opacity-0",
              )}
            />
            {reqLabel}
          </span>
        ))}
      </div>
    </div>
  );
}
