import { IconBrandGoogle } from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import { getGoogleOAuthUrl } from "@/lib/api/oauth";

export function GoogleLoginButton() {
  return (
    <a
      href={getGoogleOAuthUrl()}
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
