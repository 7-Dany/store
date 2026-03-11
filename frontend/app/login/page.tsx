import { GoogleLoginButton } from "@/components/auth/google-login-button";
import { TelegramLoginButton } from "@/components/auth/telegram-login-button";
import { ThemeToggle } from "@/components/theme-toggle";
import { IconShoppingBag } from "@tabler/icons-react";

export default function LoginPage() {
  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center bg-background px-4">
      {/* Theme toggle — top-right corner */}
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>

      {/* Card */}
      <div className="w-full max-w-[340px] space-y-6">
        {/* Logo + heading */}
        <div className="flex flex-col items-center gap-3 text-center">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl border border-border bg-card shadow-xs">
            <IconShoppingBag
              size={22}
              stroke={1.75}
              className="text-foreground"
            />
          </div>
          <div className="space-y-1">
            <h1 className="text-xl font-semibold tracking-tight text-foreground">
              Sign in to Store
            </h1>
            <p className="text-sm text-muted-foreground">
              Choose a provider to continue
            </p>
          </div>
        </div>

        {/* Provider buttons */}
        <div className="flex flex-col gap-2.5">
          <GoogleLoginButton />
          <TelegramLoginButton />
        </div>

        {/* Footer */}
        <p className="text-sm text-muted-foreground">
          By continuing, you agree to our{" "}
          <a
            href="#"
            className="underline underline-offset-4 hover:text-foreground transition-colors"
          >
            Terms of Service
          </a>{" "}
          and{" "}
          <a
            href="#"
            className="underline underline-offset-4 hover:text-foreground transition-colors"
          >
            Privacy Policy
          </a>
          .
        </p>
      </div>
    </div>
  );
}
