import type { Metadata } from "next";
import Link from "next/link";
import { AuthLayout } from "@/features/auth/components/_auth-layout";
import { ForgotPasswordForm } from "@/features/auth/components/forgot-password-form";

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

  return (
    <AuthLayout
      title="Reset your password"
      subtitle="We'll send a code to verify it's you."
      footer={
        <p className="text-sm text-muted-foreground">
          Remembered it?{" "}
          <Link href="/login" className="font-medium text-foreground underline-offset-4 transition-colors hover:underline">
            Sign in
          </Link>
        </p>
      }
    >
      <ForgotPasswordForm initialEmail={decodeURIComponent(email)} />
    </AuthLayout>
  );
}
