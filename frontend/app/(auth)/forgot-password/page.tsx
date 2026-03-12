import type { Metadata } from "next";
import Link from "next/link";
import { IconShoppingBag } from "@tabler/icons-react";
import { ThemeToggle } from "@/components/theme-toggle";
import { ForgotPasswordForm } from "@/components/auth/forgot-password-form";

export const metadata: Metadata = {
  title: "Reset password — Store",
  description: "Reset your Store account password.",
};

export default async function ForgotPasswordPage({
  searchParams,
}: {
  searchParams: Promise<{ email?: string }>;
}) {
  const { email = "" } = await searchParams;
  const initialEmail = decodeURIComponent(email);

  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center bg-background px-4">
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>

      <div className="w-full max-w-[340px]">
        {/* Logo */}
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <div className="flex size-11 items-center justify-center rounded-xl border border-border bg-card shadow-xs">
            <IconShoppingBag size={22} stroke={1.75} className="text-foreground" />
          </div>
          <div className="flex flex-col gap-1">
            <h1 className="text-xl font-semibold tracking-tight text-foreground">
              Reset your password
            </h1>
            <p className="text-sm text-muted-foreground">
              We&apos;ll send a code to verify it&apos;s you.
            </p>
          </div>
        </div>

        <ForgotPasswordForm initialEmail={initialEmail} />

        <div className="mt-6 text-center">
          <p className="text-sm text-muted-foreground">
            Remembered it?{" "}
            <Link
              href="/login"
              className="font-medium text-foreground underline-offset-4 transition-colors hover:underline"
            >
              Sign in
            </Link>
          </p>
        </div>
      </div>
    </div>
  );
}
