import type { Metadata } from "next";
import Link from "next/link";
import { AuthLayout } from "@/features/auth/components/_auth-layout";
import { VerifyEmailForm } from "@/features/auth/components/verify-email-form";

export const metadata: Metadata = {
  title: "Verify your email — Store",
  description: "Enter the code we sent to your email to activate your account.",
};

export default async function VerifyEmailPage({
  searchParams,
}: {
  searchParams: Promise<{ email?: string }>;
}) {
  const { email = "" } = await searchParams;

  return (
    <AuthLayout
      title="Verify your email"
      subtitle="Your email isn't verified yet. We've sent a code — check your inbox."
      footer={
        <p className="text-sm text-muted-foreground">
          Back to{" "}
          <Link href="/login" className="font-medium text-foreground underline-offset-4 transition-colors hover:underline">
            Sign in
          </Link>
        </p>
      }
    >
      <VerifyEmailForm initialEmail={decodeURIComponent(email)} />
    </AuthLayout>
  );
}
