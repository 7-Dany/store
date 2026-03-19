import type { Metadata } from "next";
import Link from "next/link";
import { AuthLayout } from "@/features/auth/components/_auth-layout";
import { GoogleLoginButton } from "@/features/auth/components/google-login-button";
import { TelegramLoginButton } from "@/features/auth/components/telegram-login-button";
import { LoginForm } from "@/features/auth/components/login-form";
import { LoginNotices } from "@/features/auth/components/_login-notices";

export const metadata: Metadata = {
  title: "Sign in — Store",
  description: "Sign in to your Store account.",
};

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ verified?: string; email?: string; reset?: string }>;
}) {
  const { verified, email = "", reset, error } = await searchParams;

  return (
    <AuthLayout
      title="Sign in to Store"
      subtitle="Welcome back — enter your details to continue."
      footer={
        <>
          <p className="text-sm text-muted-foreground">
            Don&apos;t have an account?{" "}
            <Link href="/register" className="font-medium text-foreground underline-offset-4 transition-colors hover:underline">
              Sign up
            </Link>
          </p>
          <p className="mt-2 text-xs text-muted-foreground">
            By continuing you agree to our{" "}
            <a href="#" className="underline underline-offset-4 transition-colors hover:text-foreground">Terms</a>{" "}
            and{" "}
            <a href="#" className="underline underline-offset-4 transition-colors hover:text-foreground">Privacy Policy</a>.
          </p>
        </>
      }
    >
      <LoginNotices reset={!!reset} verified={!!verified} error={error} />
      <LoginForm initialIdentifier={decodeURIComponent(email)} />
      <div className="my-5 flex items-center gap-3">
        <div className="h-px flex-1 bg-border" />
        <span className="text-xs text-muted-foreground">or</span>
        <div className="h-px flex-1 bg-border" />
      </div>
      <div className="flex flex-col gap-2.5">
        <GoogleLoginButton />
        <TelegramLoginButton />
      </div>
    </AuthLayout>
  );
}
