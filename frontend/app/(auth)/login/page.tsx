import type { Metadata } from "next";
import Link from "next/link";
import { IconShoppingBag } from "@tabler/icons-react";
import { ThemeToggle } from "@/components/theme-toggle";
import { GoogleLoginButton } from "@/components/auth/google-login-button";
import { TelegramLoginButton } from "@/components/auth/telegram-login-button";
import { LoginForm } from "@/components/auth/login-form";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { IconCircleCheckFilled } from "@tabler/icons-react";

export const metadata: Metadata = {
  title: "Sign in — Store",
  description: "Sign in to your Store account.",
};

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ verified?: string; email?: string; reset?: string }>;
}) {
  const { verified, email = "", reset } = await searchParams;

  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center bg-background px-4">
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>

      <div className="w-full max-w-[340px]">
        {/* Logo */}
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <div className="flex size-11 items-center justify-center rounded-xl border border-border bg-card shadow-xs">
            <IconShoppingBag
              size={22}
              stroke={1.75}
              className="text-foreground"
            />
          </div>
          <div className="flex flex-col gap-1">
            <h1 className="text-xl font-semibold tracking-tight text-foreground">
              Sign in to Store
            </h1>
            <p className="text-sm text-muted-foreground">
              Welcome back — enter your details to continue.
            </p>
          </div>
        </div>

        {/* Password reset notice */}
        {reset && (
          <Alert className="w-full mb-4 animate-in fade-in border-primary/30 bg-primary/10 text-muted-foreground  duration-300">
            <IconCircleCheckFilled size={16} />
            <AlertDescription className="w-full flex align-middle gap-2">
              <span className="w-full">
                Password reset! Sign in with new password.
              </span>
            </AlertDescription>
          </Alert>
        )}

        <LoginForm initialIdentifier={decodeURIComponent(email)} />

        {/* Divider */}
        <div className="my-5 flex items-center gap-3">
          <div className="h-px flex-1 bg-border" />
          <span className="text-xs text-muted-foreground">or</span>
          <div className="h-px flex-1 bg-border" />
        </div>

        {/* OAuth */}
        <div className="flex flex-col gap-2.5">
          <GoogleLoginButton />
          <TelegramLoginButton />
        </div>

        {/* Footer links */}
        <div className="mt-6 flex flex-col gap-2 text-center">
          <p className="text-sm text-muted-foreground">
            Don&apos;t have an account?{" "}
            <Link
              href="/register"
              className="font-medium text-foreground underline-offset-4 transition-colors hover:underline"
            >
              Sign up
            </Link>
          </p>
          <p className="text-xs text-muted-foreground">
            By continuing you agree to our{" "}
            <a
              href="#"
              className="underline underline-offset-4 transition-colors hover:text-foreground"
            >
              Terms
            </a>{" "}
            and{" "}
            <a
              href="#"
              className="underline underline-offset-4 transition-colors hover:text-foreground"
            >
              Privacy Policy
            </a>
            .
          </p>
        </div>
      </div>
    </div>
  );
}
