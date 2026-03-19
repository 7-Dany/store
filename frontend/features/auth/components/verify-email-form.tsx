"use client";

import { useEffect } from "react";
import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import { Field, FieldError } from "@/components/ui/field";
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp";
import { REGEXP_ONLY_DIGITS } from "input-otp";
import { useVerifyEmail } from "@/features/auth/hooks/use-verify-email";
import { useResendVerification } from "@/features/auth/hooks/use-resend-verification";
import { IconCheck, IconLoader2, IconMail, IconRefresh } from "@tabler/icons-react";

const schema = z.object({
  otp: z.string().length(6, "Enter all 6 digits of the code."),
});

type FormData = z.infer<typeof schema>;

interface Props { initialEmail: string; }

export function VerifyEmailForm({ initialEmail }: Props) {
  const form = useForm<FormData>({
    resolver: zodResolver(schema),
    defaultValues: { otp: "" },
  });

  const { resend, isPending: resending, countdown } = useResendVerification();
  // A code was already sent by useLogin before the redirect — start cooldown immediately.
  useEffect(() => { countdown.start(120); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const { execute: verify, isPending: verifying } = useVerifyEmail({
    onError: (msg) => form.setError("otp", { message: msg }),
  });

  const isPending = verifying || resending;

  return (
    <form onSubmit={form.handleSubmit((d) => verify(initialEmail, d.otp))} noValidate className="flex flex-col gap-5">
      <div className="flex flex-col gap-1.5">
        <h2 className="text-base font-semibold text-foreground">Check your inbox</h2>
        <p className="text-sm text-muted-foreground">
          We sent a 6-digit code to your email. Enter it below to verify your account.
        </p>
        {initialEmail && (
          <span className="mt-0.5 block truncate text-sm font-medium text-foreground" title={initialEmail}>
            {initialEmail}
          </span>
        )}
        <div className="mt-3">
          <Controller name="otp" control={form.control} render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <InputOTP maxLength={6} pattern={REGEXP_ONLY_DIGITS} value={field.value} onChange={(v) => { form.clearErrors("otp"); field.onChange(v); }} disabled={verifying} containerClassName="w-full">
                <InputOTPGroup className="w-full">
                  {[0,1,2,3,4,5].map((i) => <InputOTPSlot key={i} index={i} aria-invalid={fieldState.invalid} className="flex-1 h-9" />)}
                </InputOTPGroup>
              </InputOTP>
              {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
            </Field>
          )} />
          <div className="mt-3 flex items-center justify-center gap-1.5">
            <IconMail size={13} stroke={1.75} className="text-muted-foreground" />
            <span className="text-xs text-muted-foreground">Didn&apos;t receive it?</span>
            {countdown.isActive ? (
              <span className="text-xs text-muted-foreground">Resend in {countdown.formatted}</span>
            ) : (
              <button type="button" onClick={() => { resend(initialEmail); form.reset(); form.clearErrors(); }} disabled={resending} className="flex items-center gap-0.5 text-xs text-primary transition-opacity hover:opacity-75 disabled:opacity-50">
                {resending && <IconRefresh size={11} stroke={2} className="animate-spin" />}
                Resend code
              </button>
            )}
          </div>
        </div>
      </div>
      <Button type="submit" size="lg" disabled={isPending} className="w-full">
        {verifying
          ? <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />Verifying…</>
          : <><IconCheck size={16} stroke={2} data-icon="inline-start" />Verify email</>}
      </Button>
    </form>
  );
}
