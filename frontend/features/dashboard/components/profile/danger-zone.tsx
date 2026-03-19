"use client";

import { useEffect, useState } from "react";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp";
import { IconTrash, IconLoader2, IconMailCheck, IconCircleCheck } from "@tabler/icons-react";
import { useDeleteAccount } from "@/features/dashboard/hooks/use-delete-account";
import { useCountdown } from "@/hooks/shared/use-countdown";

export function DangerZone() {
  const [open, setOpen] = useState(false);

  return (
    <>
      <div className="flex items-start justify-between gap-6 rounded-xl border border-destructive/25 bg-destructive/[0.03] dark:bg-destructive/5 p-4">
        <div className="flex flex-col gap-0.5">
          <p className="text-sm font-medium text-foreground">Delete account</p>
          <p className="text-xs text-muted-foreground leading-relaxed">
            Schedules your account for permanent deletion after a 30-day grace period.
            All data, sessions, and connected accounts will be removed.
          </p>
        </div>
        <Button variant="destructive" size="sm" onClick={() => setOpen(true)} className="shrink-0">
          <IconTrash size={14} stroke={2} data-icon="inline-start" />
          Delete
        </Button>
      </div>
      <DeleteAccountDialog open={open} onOpenChange={setOpen} />
    </>
  );
}

function DeleteAccountDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const countdown = useCountdown();

  const { state, start, confirmWithPassword, confirmWithOtp, reset } =
    useDeleteAccount({
      onDeleted: () => setTimeout(() => { window.location.href = "/api/auth/logout"; }, 2000),
      onOtpSent: (expiresIn) => countdown.start(expiresIn),
    });

  // Base UI only fires onOpenChange when the user tries to close (Escape /
  // backdrop) — not when the `open` prop flips to true. Use an effect instead.
  useEffect(() => {
    if (open) {
      start();
    } else {
      reset();
      countdown.reset();
    }
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  function handleOpenChange(v: boolean) {
    if (!v && (state.step === "deleting" || state.step === "done")) return;
    onOpenChange(v);
  }

  function handleClose() {
    if (state.step === "deleting" || state.step === "done") return;
    handleOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent showCloseButton={state.step !== "deleting" && state.step !== "done"}>
        {(state.step === "loading_method" || state.step === "confirm_otp_init") && (
          <SpinnerStep label="Preparing…" />
        )}
        {state.step === "confirm_password" && (
          <PasswordStep onConfirm={confirmWithPassword} onCancel={handleClose} error={state.error} />
        )}
        {state.step === "confirm_otp" && (
          <OtpStep onConfirm={confirmWithOtp} onCancel={handleClose} error={state.error} countdown={countdown} />
        )}
        {state.step === "deleting" && <SpinnerStep label="Scheduling account deletion…" />}
        {state.step === "done" && <DoneStep />}
        {state.step === "error" && state.error && (
          <ErrorStep error={state.error} onClose={handleClose} />
        )}
      </DialogContent>
    </Dialog>
  );
}

function SpinnerStep({ label }: { label: string }) {
  return (
    <div className="flex flex-col items-center gap-4 py-8">
      <IconLoader2 size={28} stroke={1.5} className="animate-spin text-muted-foreground" />
      <p className="text-sm text-muted-foreground">{label}</p>
    </div>
  );
}

function DoneStep() {
  return (
    <div className="flex flex-col items-center gap-4 py-8">
      <div className="flex size-12 items-center justify-center rounded-full bg-destructive/10">
        <IconCircleCheck size={24} stroke={1.5} className="text-destructive" />
      </div>
      <div className="text-center">
        <p className="font-medium text-foreground">Deletion scheduled</p>
        <p className="mt-1 text-sm text-muted-foreground">
          Your account will be permanently deleted in 30 days. Redirecting…
        </p>
      </div>
    </div>
  );
}

function ErrorStep({ error, onClose }: { error: string; onClose: () => void }) {
  return (
    <>
      <DialogHeader>
        <DialogTitle>Something went wrong</DialogTitle>
        <DialogDescription>{error}</DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onClose}>Close</Button>
      </DialogFooter>
    </>
  );
}

function PasswordStep({
  onConfirm, onCancel, error,
}: {
  onConfirm: (p: string) => void; onCancel: () => void; error: string | null;
}) {
  const [value, setValue] = useState("");

  return (
    <>
      <DialogHeader>
        <div className="flex size-10 items-center justify-center rounded-full bg-destructive/10 mb-1">
          <IconTrash size={18} stroke={1.75} className="text-destructive" />
        </div>
        <DialogTitle>Delete your account?</DialogTitle>
        <DialogDescription>
          This schedules your account for permanent deletion after a 30-day grace period.
          Enter your password to confirm.
        </DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-2">
        <Input
          ref={(el) => el?.focus()}
          type="password"
          placeholder="Enter your password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && value && onConfirm(value)}
          aria-invalid={!!error}
        />
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onCancel}>Cancel</Button>
        <Button variant="destructive" size="sm" disabled={!value} onClick={() => onConfirm(value)}>
          Delete account
        </Button>
      </DialogFooter>
    </>
  );
}

function OtpStep({
  onConfirm, onCancel, error, countdown,
}: {
  onConfirm: (c: string) => void; onCancel: () => void;
  error: string | null; countdown: ReturnType<typeof useCountdown>;
}) {
  const [otp, setOtp] = useState("");

  return (
    <>
      <DialogHeader>
        <div className="flex size-10 items-center justify-center rounded-full bg-destructive/10 mb-1">
          <IconMailCheck size={18} stroke={1.75} className="text-destructive" />
        </div>
        <DialogTitle>Check your email</DialogTitle>
        <DialogDescription>
          We&apos;ve sent a 6-digit code to your email. Enter it below to confirm account deletion.
        </DialogDescription>
      </DialogHeader>
      <div className="flex flex-col items-center gap-3">
        <InputOTP maxLength={6} value={otp} onChange={setOtp} onComplete={onConfirm}>
          <InputOTPGroup>
            {[0,1,2,3,4,5].map((i) => <InputOTPSlot key={i} index={i} />)}
          </InputOTPGroup>
        </InputOTP>
        {error && <p className="text-xs text-destructive">{error}</p>}
        {countdown.isActive && (
          <p className="text-xs text-muted-foreground">
            Expires in{" "}
            <span className="font-medium tabular-nums text-foreground">{countdown.formatted}</span>
          </p>
        )}
      </div>
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onCancel}>Cancel</Button>
        <Button variant="destructive" size="sm" disabled={otp.length < 6} onClick={() => onConfirm(otp)}>
          Confirm deletion
        </Button>
      </DialogFooter>
    </>
  );
}
