"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { IconLoader2, IconCircleCheck } from "@tabler/icons-react";
import { useChangePassword } from "@/features/dashboard/hooks/use-change-password";
import { useSetPassword } from "@/features/dashboard/hooks/use-set-password";
import { validatePassword } from "@/lib/auth/password";
import {
  PasswordInput,
  LabeledField,
} from "@/features/dashboard/components/shared/form-components";

export function ChangePasswordCard() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  const { changePassword, isPending } = useChangePassword({
    onSuccess: () => { setCurrent(""); setNext(""); setConfirm(""); setErrors({}); },
  });

  function handleSubmit() {
    const e: Record<string, string> = {};
    if (!current) e.current = "Current password is required.";
    const pwErr = validatePassword(next);
    if (pwErr) e.next = pwErr;
    if (next && confirm && next !== confirm) e.confirm = "Passwords do not match.";
    setErrors(e);
    if (Object.keys(e).length) return;
    changePassword(current, next);
  }

  return (
    <div className="flex flex-col gap-4">
      <LabeledField id="current-pw" label="Current password" error={errors.current}>
        <PasswordInput id="current-pw" value={current} onChange={setCurrent} placeholder="Enter current password" aria-invalid={!!errors.current} autoComplete="current-password" />
      </LabeledField>
      <Separator />
      <LabeledField id="new-pw" label="New password" error={errors.next}>
        <PasswordInput id="new-pw" value={next} onChange={setNext} placeholder="Min. 8 characters" aria-invalid={!!errors.next} />
      </LabeledField>
      <LabeledField id="confirm-pw" label="Confirm new password" error={errors.confirm}>
        <PasswordInput id="confirm-pw" value={confirm} onChange={setConfirm} placeholder="Repeat new password" aria-invalid={!!errors.confirm} />
      </LabeledField>
      <div className="pt-1">
        <Button size="sm" onClick={handleSubmit} disabled={isPending || !current || !next || !confirm}>
          {isPending && <IconLoader2 size={14} stroke={2} className="animate-spin" data-icon="inline-start" />}
          Update password
        </Button>
      </div>
    </div>
  );
}

export function SetPasswordCard() {
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState(false);
  const { setPassword, isPending } = useSetPassword({ onSuccess: () => setDone(true) });

  function handleSubmit() {
    const e: Record<string, string> = {};
    const pwErr = validatePassword(next);
    if (pwErr) e.next = pwErr;
    if (next && confirm && next !== confirm) e.confirm = "Passwords do not match.";
    setErrors(e);
    if (Object.keys(e).length) return;
    setPassword(next);
  }

  if (done) {
    return (
      <div className="flex items-center gap-3 text-sm">
        <div className="flex size-8 items-center justify-center rounded-full bg-primary/10">
          <IconCircleCheck size={16} stroke={2} className="text-primary" />
        </div>
        <div>
          <p className="font-medium text-foreground">Password set</p>
          <p className="text-xs text-muted-foreground">You can now sign in with your email and password.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <LabeledField id="set-pw" label="New password" error={errors.next}>
        <PasswordInput id="set-pw" value={next} onChange={setNext} placeholder="Min. 8 characters" aria-invalid={!!errors.next} />
      </LabeledField>
      <LabeledField id="set-pw-confirm" label="Confirm password" error={errors.confirm}>
        <PasswordInput id="set-pw-confirm" value={confirm} onChange={setConfirm} placeholder="Repeat password" aria-invalid={!!errors.confirm} />
      </LabeledField>
      <div className="pt-1">
        <Button size="sm" onClick={handleSubmit} disabled={isPending || !next || !confirm}>
          {isPending && <IconLoader2 size={14} stroke={2} className="animate-spin" data-icon="inline-start" />}
          Set password
        </Button>
      </div>
    </div>
  );
}
