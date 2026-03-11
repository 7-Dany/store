import Link from "next/link";
import { cookies } from "next/headers";

// Protected page — only reachable when the session cookie is present.
// proxy.ts redirects unauthenticated visitors to /login.
//
// After a real Google OAuth flow the URL will have:
//   ?provider=google&action=login   (new login or register via Google)
//   ?provider=google&action=linked  (account link — no session cookie set yet)
export default async function DashboardPage({
  searchParams,
}: {
  searchParams: Promise<{ provider?: string; action?: string; error?: string }>;
}) {
  const { provider, action } = await searchParams;
  const cookieStore = await cookies();
  const session = cookieStore.get("session");

  const isOAuth  = !!provider && provider !== "mock";
  const isMock   = !isOAuth;
  const providerLabel = provider ? provider.charAt(0).toUpperCase() + provider.slice(1) : "OAuth";
  const authLabel = isMock
    ? "Mock (dev only)"
    : `${providerLabel} OAuth — ${action === "linked" ? "account linked" : "signed in"}`;

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <div className="w-full max-w-sm space-y-6 text-center">

        {/* ── Status badge ── */}
        <div className="inline-flex items-center gap-2 rounded-full border border-green-200 bg-green-50 px-3 py-1 text-xs font-medium text-green-700 dark:border-green-900 dark:bg-green-950 dark:text-green-400">
          <span className="h-1.5 w-1.5 rounded-full bg-green-500" />
          Session active
        </div>

        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
          <p className="text-sm text-muted-foreground">
            Protected by{" "}
            <code className="font-mono text-xs">proxy.ts</code>. You reached it
            because a valid session cookie is present.
          </p>
        </div>

        {/* ── Info card ── */}
        <div className="rounded-2xl border bg-card p-5 text-left shadow-sm space-y-3 text-sm">
          <Row label="Route"       value="/dashboard" />
          <Row label="Guard"       value="proxy.ts → cookie: session" />
          <Row label="Auth method" value={authLabel} />
          {session && (
            <Row
              label="Token (truncated)"
              value={session.value.slice(0, 24) + "…"}
              mono
            />
          )}
          {isOAuth && (
            <Row label="Provider" value={provider!} />
          )}
        </div>

        {/* ── Actions ── */}
        <div className="flex flex-col gap-2">
          <Link
            href="/api/mock-logout"
            className="flex w-full items-center justify-center rounded-lg border border-border bg-background px-4 py-2.5 text-sm font-medium text-foreground shadow-sm transition-colors hover:bg-muted"
          >
            Sign out
          </Link>
          <Link
            href="/login"
            className="text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            ← Back to login
          </Link>
        </div>

      </div>
    </div>
  );
}

function Row({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span
        className={`text-right font-medium truncate ${mono ? "font-mono text-xs text-primary" : ""}`}
      >
        {value}
      </span>
    </div>
  );
}
