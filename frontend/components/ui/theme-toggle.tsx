"use client";

import { useTheme } from "next-themes";
import { IconSun, IconMoon, IconDeviceDesktop } from "@tabler/icons-react";
import { cn } from "@/lib/utils";

const themes = [
  { key: "light",  Icon: IconSun,           label: "Light" },
  { key: "dark",   Icon: IconMoon,          label: "Dark" },
  { key: "system", Icon: IconDeviceDesktop, label: "System" },
] as const;

export function ThemeToggle({ className }: { className?: string }) {
  const { theme, setTheme } = useTheme();

  return (
    <div
      className={cn(
        "inline-flex items-center gap-0.5 rounded-lg border border-border bg-muted p-1",
        className,
      )}
      role="group"
      aria-label="Toggle theme"
    >
      {themes.map(({ key, Icon, label }) => (
        <button
          key={key}
          onClick={() => setTheme(key)}
          title={label}
          // suppressHydrationWarning — aria-pressed depends on theme which is
          // client-only; server always renders unselected, client corrects it.
          aria-pressed={theme === key}
          suppressHydrationWarning
          className={cn(
            "inline-flex h-7 w-7 items-center justify-center rounded-md transition-colors duration-150",
            theme === key
              ? "bg-background text-foreground shadow-xs"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          <Icon size={15} stroke={1.75} />
          <span className="sr-only">{label}</span>
        </button>
      ))}
    </div>
  );
}
