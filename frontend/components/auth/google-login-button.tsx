import { IconBrandGoogle } from "@tabler/icons-react";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080/api/v1";
const GOOGLE_OAUTH_URL = `${API_BASE}/oauth/google`;

export function GoogleLoginButton() {
  return (
    <a
      href={GOOGLE_OAUTH_URL}
      className="
        group flex w-full items-center justify-center gap-3
        rounded-lg border border-border bg-card
        px-4 h-10
        text-sm font-medium text-foreground
        shadow-xs transition-colors duration-150
        hover:bg-muted active:scale-[0.99]
      "
    >
      <IconBrandGoogle size={18} stroke={1.75} className="shrink-0" />
      Continue with Google
    </a>
  );
}
