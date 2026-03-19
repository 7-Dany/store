"use client";

import { use } from "react";
import { AuthLayout } from "@/features/auth/components/_auth-layout";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp";
import { REGEXP_ONLY_DIGITS } from "input-otp";
import { StepDots } from "@/features/auth/components/_step-dots";
import { useUnlock } from "@/features/auth/hooks/use-unlock";
import { cn } from "@/lib/utils";
import { useState } from "react";
import {
  IconArrowRight, IconArrowLeft, IconCheck,
  IconLoader2, IconMail, IconRefresh, IconLockOpen,
} from "@tabler/icons-react";

// This page is client-rendered so we can read searchParams directly.
export default function UnlockPage({
  searchParams,
}: {
  searchParams: Promise<{ email?: string }>;
}) {
  const { email: initialEmail = "" } = use(searchParams);
  return (
    <AuthLayout
      title="Unlock your account"
      subtitle="Too many failed login attempts? We'll send a 6-digit code to get you back in."
      footer={
        <p className="text-sm text-muted-foreground">
          Remember your password?{" "}
          <Link
            href="/login"
            className="font-medium text-foreground underline-offset-4 transition-colors hover:underline"
          >
            Sign in
          </Link>
        </p>
      }
    >
      <UnlockForm initialEmail={initialEmail} />
    </AuthLayout>
  );
}

function UnlockForm({ initialEmail }: { initialEmail: string }) {
  const [emailInput, setEmailInput] = useState(initialEmail);
  const [emailError, setEmailError] = useState("");
  const [otpValue, setOtpValue] = useState("");

  const { state, isPending, countdown, requestCode, resendCode, confirmCode } =
    useUnlock(initialEmail);

  const animClass = "animate-in slide-in-from-right-4 fade-in";

  // ── Step: enter email ──────────────────────────────────────────────────────

  if (state.step === "email") {
    function handleSubmit() {
      const e = emailInput.trim();
      if (!e) { setEmailError("Please enter your email address."); return; }
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e)) {
        setEmailError("Please enter a valid email address.");
        return;
      }
      setEmailError("");
      requestCode(e);
    }

    return (
      <div className="flex flex-col gap-5">
        <div className="grid grid-cols-3 items-center">
          <span />
          <StepDots total={2} current={0} className="justify-self-center" />
        </div>
        <div key="email" className={cn("flex flex-col gap-4", animClass)} style={{ animationDuration: "200ms" }}>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-muted-foreground" htmlFor="unlock-email">
              Email address
            </label>
            <Input
              id="unlock-email"
              type="email"
              autoComplete="email"
              placeholder="you@example.com"
              value={emailInput}
              onChange={(e) => { setEmailInput(e.target.value); setEmailError(""); }}
              onKeyDown={(e) => e.key === "Enter" && handleSubmit()}
              aria-invalid={!!emailError}
              disabled={isPending}
            />
            {emailError && <p className="text-xs text-destructive">{emailError}</p>}
            {state.error && <p className="text-xs text-destructive">{state.error}</p>}
          </div>
        </div>
        <Button size="lg" className="w-full" onClick={handleSubmit} disabled={isPending}>
          {isPending
            ? <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />Sending code…</>
            : <>Send unlock code <IconArrowRight size={16} stroke={2} data-icon="inline-end" /></>}
        </Button>
      </div>
    );
  }

  // ── Step: enter OTP ────────────────────────────────────────────────────────

  if (state.step === "code") {
    return (
      <div className="flex flex-col gap-5">
        <div className="grid grid-cols-3 items-center">
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="-ml-0.5 flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground justify-self-start"
          >
            <IconArrowLeft size={12} stroke={2} /> Back
          </button>
          <StepDots total={2} current={1} className="justify-self-center" />
        </div>
        <div key="code" className={cn("flex flex-col gap-4", animClass)} style={{ animationDuration: "200ms" }}>
          <div className="flex flex-col gap-1.5">
            <p className="text-sm text-muted-foreground">
              We sent a 6-digit unlock code to{" "}
              <span className="font-medium text-foreground">{state.email}</span>.
            </p>
            <InputOTP
              maxLength={6}
              pattern={REGEXP_ONLY_DIGITS}
              value={otpValue}
              onChange={(v) => { setOtpValue(v); }}
              onComplete={confirmCode}
              disabled={isPending}
              containerClassName="w-full mt-2"
            >
              <InputOTPGroup className="w-full">
                {[0, 1, 2, 3, 4, 5].map((i) => (
                  <InputOTPSlot key={i} index={i} className="flex-1 h-9" />
                ))}
              </InputOTPGroup>
            </InputOTP>
            {state.error && <p className="mt-1 text-xs text-destructive">{state.error}</p>}
            <div className="mt-2 flex items-center justify-center gap-1.5">
              <IconMail size={13} stroke={1.75} className="text-muted-foreground" />
              <span className="text-xs text-muted-foreground">Didn&apos;t receive it?</span>
              {countdown.isActive ? (
                <span className="text-xs text-muted-foreground">Resend in {countdown.formatted}</span>
              ) : (
                <button
                  type="button"
                  onClick={() => { setOtpValue(""); resendCode(); }}
                  disabled={isPending}
                  className="flex items-center gap-0.5 text-xs text-primary transition-opacity hover:opacity-75 disabled:opacity-50"
                >
                  {isPending && <IconRefresh size={11} stroke={2} className="animate-spin" />}
                  Resend code
                </button>
              )}
            </div>
          </div>
        </div>
        <Button
          size="lg"
          className="w-full"
          onClick={() => confirmCode(otpValue)}
          disabled={isPending || otpValue.length < 6}
        >
          {isPending
            ? <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />Unlocking…</>
            : <><IconCheck size={16} stroke={2} data-icon="inline-start" />Unlock account</>}
        </Button>
      </div>
    );
  }

  // ── Step: done ─────────────────────────────────────────────────────────────

  return (
    <div className="flex flex-col items-center gap-4 py-4 text-center">
      <div className="flex size-12 items-center justify-center rounded-full bg-primary/10">
        <IconLockOpen size={22} stroke={1.75} className="text-primary" />
      </div>
      <div className="flex flex-col gap-1">
        <p className="font-semibold text-foreground">Account unlocked!</p>
        <p className="text-sm text-muted-foreground">Redirecting you to sign in…</p>
      </div>
    </div>
  );
}
