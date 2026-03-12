import { IconBrandGoogle } from "@tabler/icons-react";
import { cn } from "@/lib/utils";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080/api/v1";
const GOOGLE_OAUTH_URL = `${API_BASE}/oauth/google`;

export function GoogleLoginButton() {
  return (
    <a
      href={GOOGLE_OAUTH_URL}
      className={cn(
        "flex w-full items-center justify-center gap-3",
        "h-10 rounded-4xl border border-border bg-input/30 px-4",
        "text-sm font-medium text-muted-foreground",
        "transition-colors duration-150",
        "hover:bg-input/50 active:scale-[0.99]",
      )}
    >
      <IconBrandGoogle
        size={18}
        stroke={1.75}
        className="shrink-0 text-muted-foreground"
      />
      Continue with Google
    </a>
  );
}
