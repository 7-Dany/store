import type { Metadata } from "next";
import Link from "next/link";
import { IconShoppingBag } from "@tabler/icons-react";
import { ThemeToggle } from "@/components/theme-toggle";
import { RegisterForm } from "@/components/auth/register-form";

export const metadata: Metadata = {
  title: "Create account — Store",
  description: "Create a new Store account.",
};

export default function RegisterPage() {
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
              Create your account
            </h1>
            <p className="text-sm text-muted-foreground">
              One step at a time — it only takes a minute.
            </p>
          </div>
        </div>

        <RegisterForm />

        <div className="mt-6 text-center">
          <p className="text-sm text-muted-foreground">
            Already have an account?{" "}
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
