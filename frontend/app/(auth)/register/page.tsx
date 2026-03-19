import type { Metadata } from "next";
import Link from "next/link";
import { AuthLayout } from "@/features/auth/components/_auth-layout";
import { RegisterForm } from "@/features/auth/components/register-form";

export const metadata: Metadata = {
  title: "Create account — Store",
  description: "Create a new Store account.",
};

export default function RegisterPage() {
  return (
    <AuthLayout
      title="Create your account"
      subtitle="One step at a time — it only takes a minute."
      footer={
        <p className="text-sm text-muted-foreground">
          Already have an account?{" "}
          <Link href="/login" className="font-medium text-foreground underline-offset-4 transition-colors hover:underline">
            Sign in
          </Link>
        </p>
      }
    >
      <RegisterForm />
    </AuthLayout>
  );
}
