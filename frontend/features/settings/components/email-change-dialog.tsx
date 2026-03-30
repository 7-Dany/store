"use client";

import { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp";
import { REGEXP_ONLY_DIGITS } from "input-otp";
import {
  IconLoader2,
  IconMail,
  IconRefresh,
  IconCircleCheck,
  IconArrowRight,
} from "@tabler/icons-react";
import { useChangeEmail } from "@/features/settings/hooks/use-change-email";

interface EmailChangeDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  currentEmail: string;
  onSuccess: (newEmail: string) => void;
}

export function EmailChangeDialog({
  open,
  onOpenChange,
  currentEmail,
  onSuccess,
}: EmailChangeDialogProps) {
  const {
    state,
    countdown,
    reset,
    clearError,
    requestChange,
    resendCurrentOtp,
    verifyCurrentOtp,
    confirmChange,
  } = useChangeEmail();

  // Reset the form state whenever the dialog opens.
  useEffect(() => {
    if (open) reset();
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  // Notify parent after done — use effect to avoid calling setState during render.
  useEffect(() => {
    if (state.step === "done") {
      onSuccess(state.newEmail);
    }
  }, [state.step]); // eslint-disable-line react-hooks/exhaustive-deps

  function handleOpenChange(v: boolean) {
    // Block closing while a request is in-flight or during the done redirect.
    if (!v && (state.loading || state.step === "done")) return;
    if (!v) reset();
    onOpenChange(v);
  }

  const isDone = state.step === "done";

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent showCloseButton={!state.loading && !isDone}>
        {state.step === "request" && (
          <RequestStep
            currentEmail={currentEmail}
            loading={state.loading}
            error={state.error}
            onClearError={clearError}
            onSubmit={requestChange}
            onCancel={() => handleOpenChange(false)}
          />
        )}
        {state.step === "otp_current" && (
          <OtpCurrentStep
            isPending={state.loading}
            error={state.error}
            countdown={countdown}
            onClearError={clearError}
            onSubmit={verifyCurrentOtp}
            onResend={resendCurrentOtp}
            onCancel={() => handleOpenChange(false)}
          />
        )}
        {state.step === "otp_new" && (
          <OtpNewStep
            newEmail={state.newEmail}
            isPending={state.loading}
            error={state.error}
            countdown={countdown}
            onClearError={clearError}
            onSubmit={confirmChange}
            onCancel={() => handleOpenChange(false)}
          />
        )}
        {isDone && <DoneStep />}
      </DialogContent>
    </Dialog>
  );
}

// ─── Steps ────────────────────────────────────────────────────────────────────

function RequestStep({
  currentEmail, loading, error, onClearError, onSubmit, onCancel,
}: {
  currentEmail: string;
  loading: boolean;
  error: string | null;
  onClearError: () => void;
  onSubmit: (email: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState("");
  const [localError, setLocalError] = useState("");

  function handleSubmit() {
    const v = value.trim();
    if (!v) { setLocalError("Please enter a new email address."); return; }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)) {
      setLocalError("Please enter a valid email address.");
      return;
    }
    if (v.toLowerCase() === currentEmail.toLowerCase()) {
      setLocalError("This is already your current email address.");
      return;
    }
    setLocalError("");
    onSubmit(v);
  }

  const displayError = localError || error;

  return (
    <>
      <DialogHeader>
        <DialogTitle>Change email address</DialogTitle>
        <DialogDescription>
          Enter your new address. We&apos;ll send a verification code to your{" "}
          <span className="font-medium text-foreground">current</span> email (
          {currentEmail}) to confirm it&apos;s you.
        </DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-1.5">
        <label className="text-xs font-medium text-muted-foreground" htmlFor="new-email-input">
          New email address
        </label>
        <Input
          id="new-email-input"
          ref={(el) => el?.focus()}
          type="email"
          placeholder="new@example.com"
          value={value}
          onChange={(e) => { setValue(e.target.value); setLocalError(""); onClearError(); }}
          onKeyDown={(e) => e.key === "Enter" && handleSubmit()}
          aria-invalid={!!displayError}
          disabled={loading}
        />
        {displayError && <p className="text-xs text-destructive">{displayError}</p>}
      </div>
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onCancel} disabled={loading}>
          Cancel
        </Button>
        <Button size="sm" onClick={handleSubmit} disabled={loading || !value.trim()}>
          {loading
            ? <IconLoader2 size={14} stroke={2} className="animate-spin" data-icon="inline-start" />
            : <IconArrowRight size={14} stroke={2} data-icon="inline-end" />}
          Send code
        </Button>
      </DialogFooter>
    </>
  );
}

function OtpCurrentStep({
  isPending, error, countdown, onClearError, onSubmit, onResend, onCancel,
}: {
  isPending: boolean;
  error: string | null;
  countdown: ReturnType<typeof import("@/features/shared/hooks/use-countdown").useCountdown>;
  onClearError: () => void;
  onSubmit: (code: string) => void;
  onResend: () => void;
  onCancel: () => void;
}) {
  const [otp, setOtp] = useState("");

  return (
    <>
      <DialogHeader>
        <DialogTitle>Check your current email</DialogTitle>
        <DialogDescription>
          We sent a 6-digit code to your <span className="font-medium text-foreground">current</span>{" "}
          email address. Enter it below to confirm it&apos;s you.
        </DialogDescription>
      </DialogHeader>
      <div className="flex flex-col items-center gap-3">
        <InputOTP
          maxLength={6}
          pattern={REGEXP_ONLY_DIGITS}
          value={otp}
          onChange={(v) => { setOtp(v); onClearError(); }}
          onComplete={onSubmit}
          disabled={isPending}
          containerClassName="w-full"
        >
          <InputOTPGroup className="w-full">
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <InputOTPSlot key={i} index={i} className="flex-1 h-9" />
            ))}
          </InputOTPGroup>
        </InputOTP>
        {error && <p className="text-xs text-destructive">{error}</p>}
        <div className="flex items-center gap-1.5">
          <IconMail size={13} stroke={1.75} className="text-muted-foreground" />
          <span className="text-xs text-muted-foreground">Didn&apos;t receive it?</span>
          {countdown.isActive ? (
            <span className="text-xs text-muted-foreground">Resend in {countdown.formatted}</span>
          ) : (
            <button
              type="button"
              onClick={() => { setOtp(""); onResend(); }}
              disabled={isPending}
              className="flex items-center gap-0.5 text-xs text-primary transition-opacity hover:opacity-75 disabled:opacity-50"
            >
              {isPending && <IconRefresh size={11} stroke={2} className="animate-spin" />}
              Resend
            </button>
          )}
        </div>
      </div>
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onCancel} disabled={isPending}>
          Cancel
        </Button>
        <Button size="sm" onClick={() => onSubmit(otp)} disabled={isPending || otp.length < 6}>
          {isPending && (
            <IconLoader2 size={14} stroke={2} className="animate-spin" data-icon="inline-start" />
          )}
          Verify
        </Button>
      </DialogFooter>
    </>
  );
}

function OtpNewStep({
  newEmail, isPending, error, countdown, onClearError, onSubmit, onCancel,
}: {
  newEmail: string;
  isPending: boolean;
  error: string | null;
  countdown: ReturnType<typeof import("@/features/shared/hooks/use-countdown").useCountdown>;
  onClearError: () => void;
  onSubmit: (code: string) => void;
  onCancel: () => void;
}) {
  const [otp, setOtp] = useState("");

  return (
    <>
      <DialogHeader>
        <DialogTitle>Confirm your new email</DialogTitle>
        <DialogDescription>
          We sent a 6-digit code to{" "}
          <span className="font-medium text-foreground">{newEmail}</span>.
          Enter it to complete the change.
        </DialogDescription>
      </DialogHeader>
      <div className="flex flex-col items-center gap-3">
        <InputOTP
          maxLength={6}
          pattern={REGEXP_ONLY_DIGITS}
          value={otp}
          onChange={(v) => { setOtp(v); onClearError(); }}
          onComplete={onSubmit}
          disabled={isPending}
          containerClassName="w-full"
        >
          <InputOTPGroup className="w-full">
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <InputOTPSlot key={i} index={i} className="flex-1 h-9" />
            ))}
          </InputOTPGroup>
        </InputOTP>
        {error && <p className="text-xs text-destructive">{error}</p>}
        {countdown.isActive && (
          <p className="text-xs text-muted-foreground">
            Code expires in{" "}
            <span className="font-medium tabular-nums text-foreground">{countdown.formatted}</span>
          </p>
        )}
      </div>
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onCancel} disabled={isPending}>
          Cancel
        </Button>
        <Button size="sm" onClick={() => onSubmit(otp)} disabled={isPending || otp.length < 6}>
          {isPending && (
            <IconLoader2 size={14} stroke={2} className="animate-spin" data-icon="inline-start" />
          )}
          Confirm change
        </Button>
      </DialogFooter>
    </>
  );
}

function DoneStep() {
  return (
    <div className="flex flex-col items-center gap-4 py-8 text-center">
      <div className="flex size-12 items-center justify-center rounded-full bg-primary/10">
        <IconCircleCheck size={24} stroke={1.5} className="text-primary" />
      </div>
      <div>
        <p className="font-medium text-foreground">Email updated!</p>
        <p className="mt-1 text-sm text-muted-foreground">
          All sessions have been signed out. Redirecting to sign in…
        </p>
      </div>
    </div>
  );
}
