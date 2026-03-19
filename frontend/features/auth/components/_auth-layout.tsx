import { ThemeToggle } from "@/components/ui/theme-toggle";
import { IconShoppingBag } from "@tabler/icons-react";

interface AuthLayoutProps {
  children: React.ReactNode;
  /** Page-level title shown below the logo. */
  title: string;
  /** Subtitle shown below the title. */
  subtitle: string;
  /** Optional footer slot — sign-in / sign-up links etc. */
  footer?: React.ReactNode;
}

/**
 * Shared shell for all auth pages: centered card, logo, theme toggle.
 * Pages import and use this directly — the Next.js layout.tsx just
 * renders {children} since each auth page composes its own heading.
 */
export function AuthLayout({ children, title, subtitle, footer }: AuthLayoutProps) {
  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center bg-background px-4">
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>

      <div className="w-full max-w-85">
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <div className="flex size-11 items-center justify-center rounded-xl border border-border bg-card shadow-xs">
            <IconShoppingBag size={22} stroke={1.75} className="text-foreground" />
          </div>
          <div className="flex flex-col gap-1">
            <h1 className="text-xl font-semibold tracking-tight text-foreground">
              {title}
            </h1>
            <p className="text-sm text-muted-foreground">{subtitle}</p>
          </div>
        </div>

        {children}

        {footer && <div className="mt-6 text-center">{footer}</div>}
      </div>
    </div>
  );
}
