"use client";

import { useState } from "react";
import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FieldError, FieldGroup } from "@/components/ui/field";
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp";
import { REGEXP_ONLY_DIGITS } from "input-otp";
import { PasswordStrength } from "./_password-strength";
import { StepDots } from "./_step-dots";
import { useForgotPassword } from "@/features/auth/hooks/use-forgot-password";
import { useVerifyResetCode } from "@/features/auth/hooks/use-verify-reset-code";
import { useResetPassword } from "@/features/auth/hooks/use-reset-password";
import { useForgotPasswordResend } from "@/features/auth/hooks/use-forgot-password-resend";
import {
  IconArrowRight, IconArrowLeft, IconCheck,
  IconEye, IconEyeOff, IconLoader2, IconMail, IconRefresh,
} from "@tabler/icons-react";

const emailSchema = z.object({
  email: z.string().trim().min(1, "Please enter your email address.").email("Please enter a valid email address."),
});
const otpSchema = z.object({
  otp: z.string().length(6, "Enter all 6 digits of the code."),
});
const passwordSchema = z
  .object({
    password: z.string()
      .min(8, "Password must be at least 8 characters.")
      .regex(/[A-Z]/, "Password needs an uppercase letter.")
      .regex(/[a-z]/, "Password needs a lowercase letter.")
      .regex(/[0-9]/, "Password needs a number.")
      .regex(/[^A-Za-z0-9]/, "Password needs a symbol (e.g. !@#$)."),
    confirm: z.string().min(1, "Please confirm your password."),
  })
  .refine((d) => d.password === d.confirm, { message: "Passwords don't match.", path: ["confirm"] });

type StepKey = "email" | "otp" | "password";

const STEPS: { key: StepKey; heading: string; description: (ctx: { email: string }) => string }[] = [
  { key: "email",    heading: "Forgot your password?", description: () => "Enter the email address you registered with and we'll send you a reset code." },
  { key: "otp",      heading: "Check your inbox",      description: ({ email }) => `We sent a 6-digit reset code to ${email || "your email"}.` },
  { key: "password", heading: "Create new password",   description: () => "Use 8+ characters with uppercase, lowercase, a number, and a symbol." },
];

interface ForgotPasswordFormProps { initialEmail?: string; }

export function ForgotPasswordForm({ initialEmail = "" }: ForgotPasswordFormProps) {
  const [stepIndex, setStepIndex] = useState(0);
  const [direction, setDirection] = useState<"forward" | "back">("forward");
  const [email, setEmail] = useState(initialEmail);
  const [resetToken, setResetToken] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);

  const step = STEPS[stepIndex];
  const emailForm    = useForm({ resolver: zodResolver(emailSchema),    defaultValues: { email: initialEmail } });
  const otpForm      = useForm({ resolver: zodResolver(otpSchema),      defaultValues: { otp: "" } });
  const passwordForm = useForm({ resolver: zodResolver(passwordSchema), defaultValues: { password: "", confirm: "" } });

  const { resend, isPending: resending, countdown } = useForgotPasswordResend();

  const { execute: sendCode, isPending: sending } = useForgotPassword({
    onSuccess: () => { advance(); countdown.start(60); },
    onError: (msg) => emailForm.setError("email", { message: msg }),
  });
  const { execute: verifyCode, isPending: verifying } = useVerifyResetCode({
    onSuccess: (token) => { setResetToken(token); advance(); },
    onError: (msg) => otpForm.setError("otp", { message: msg }),
  });
  const { execute: resetPwd, isPending: resetting } = useResetPassword({
    onError: (msg) => passwordForm.setError("password", { message: msg }),
  });

  const isPending = sending || verifying || resetting || resending;

  function clearAllErrors() { emailForm.clearErrors(); otpForm.clearErrors(); passwordForm.clearErrors(); }
  function advance() { clearAllErrors(); setDirection("forward"); setStepIndex((i) => i + 1); }
  function back()    { clearAllErrors(); setDirection("back");    setStepIndex((i) => Math.max(0, i - 1)); }

  const animClass = direction === "forward" ? "animate-in slide-in-from-right-4 fade-in" : "animate-in slide-in-from-left-4 fade-in";
  const passwordValue = passwordForm.watch("password");

  if (step.key === "email") {
    return (
      <form onSubmit={emailForm.handleSubmit((d) => { setEmail(d.email.trim()); sendCode(d.email.trim()); })} noValidate className="flex flex-col gap-5">
        <div className="grid grid-cols-3 items-center">
          <span /><StepDots total={STEPS.length} current={0} className="justify-self-center" />
        </div>
        <div key={stepIndex} className={cn("flex flex-col gap-1.5", animClass)} style={{ animationDuration: "200ms" }}>
          <h2 className="text-base font-semibold text-foreground">{step.heading}</h2>
          <p className="text-sm text-muted-foreground">{step.description({ email })}</p>
          <FieldGroup className="mt-3 gap-0">
            <Controller name="email" control={emailForm.control} render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <Input {...field} id="fp-email" type="email" autoComplete="email" placeholder="you@example.com" aria-invalid={fieldState.invalid} disabled={sending} onChange={(e) => { clearAllErrors(); field.onChange(e); }} />
                {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
              </Field>
            )} />
          </FieldGroup>
        </div>
        <Button type="submit" size="lg" disabled={isPending} className="w-full">
          {sending ? <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />Sending code…</> : <>Send reset code <IconArrowRight size={16} stroke={2} data-icon="inline-end" /></>}
        </Button>
      </form>
    );
  }

  if (step.key === "otp") {
    return (
      <form onSubmit={otpForm.handleSubmit((d) => verifyCode(email, d.otp))} noValidate className="flex flex-col gap-5">
        <div className="grid grid-cols-3 items-center">
          <button type="button" onClick={back} className="-ml-0.5 flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground justify-self-start"><IconArrowLeft size={12} stroke={2} /> Back</button>
          <StepDots total={STEPS.length} current={1} className="justify-self-center" />
        </div>
        <div key={stepIndex} className={cn("flex flex-col gap-1.5", animClass)} style={{ animationDuration: "200ms" }}>
          <h2 className="text-base font-semibold text-foreground">{step.heading}</h2>
          <p className="text-sm text-muted-foreground">{step.description({ email })}</p>
          <div className="mt-3">
            <Controller name="otp" control={otpForm.control} render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <InputOTP maxLength={6} pattern={REGEXP_ONLY_DIGITS} value={field.value} onChange={(v) => { clearAllErrors(); field.onChange(v); }} disabled={verifying} containerClassName="w-full">
                  <InputOTPGroup className="w-full">{[0,1,2,3,4,5].map((i) => <InputOTPSlot key={i} index={i} aria-invalid={fieldState.invalid} className="flex-1 h-9" />)}</InputOTPGroup>
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
                <button type="button" onClick={() => { resend(email); otpForm.reset(); clearAllErrors(); }} disabled={resending} className="flex items-center gap-0.5 text-xs text-primary transition-opacity hover:opacity-75 disabled:opacity-50">
                  {resending && <IconRefresh size={11} stroke={2} className="animate-spin" />}Resend code
                </button>
              )}
            </div>
          </div>
        </div>
        <Button type="submit" size="lg" disabled={isPending} className="w-full">
          {verifying ? <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />Verifying…</> : <><IconCheck size={16} stroke={2} data-icon="inline-start" />Verify code</>}
        </Button>
      </form>
    );
  }

  return (
    <form onSubmit={passwordForm.handleSubmit((d) => resetPwd(resetToken, d.password))} noValidate className="flex flex-col gap-5">
      <div className="grid grid-cols-3 items-center">
        <span /><StepDots total={STEPS.length} current={2} className="justify-self-center" />
      </div>
      <div key={stepIndex} className={cn("flex flex-col gap-1.5", animClass)} style={{ animationDuration: "200ms" }}>
        <h2 className="text-base font-semibold text-foreground">{step.heading}</h2>
        <p className="text-sm text-muted-foreground">{step.description({ email })}</p>
        <span className="mt-0.5 text-sm font-medium text-foreground">{email}</span>
        <FieldGroup className="mt-3 gap-3">
          <Controller name="password" control={passwordForm.control} render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <div className="relative">
                <Input {...field} id="new-password" type={showPassword ? "text" : "password"} autoComplete="new-password" placeholder="New password" aria-invalid={fieldState.invalid} disabled={resetting} onChange={(e) => { clearAllErrors(); field.onChange(e); }} className="pr-10" />
                <button type="button" tabIndex={-1} onClick={() => setShowPassword((p) => !p)} aria-label={showPassword ? "Hide password" : "Show password"} className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground">
                  {showPassword ? <IconEyeOff size={16} stroke={1.75} /> : <IconEye size={16} stroke={1.75} />}
                </button>
              </div>
              <PasswordStrength value={passwordValue} />
              {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
            </Field>
          )} />
          <Controller name="confirm" control={passwordForm.control} render={({ field, fieldState }) => {
            const matches = field.value.length > 0 && field.value === passwordValue;
            const mismatch = field.value.length > 0 && field.value !== passwordValue;
            return (
              <Field data-invalid={fieldState.invalid}>
                <div className="relative">
                  <Input {...field} id="confirm-password" type={showConfirm ? "text" : "password"} autoComplete="new-password" placeholder="Confirm new password" aria-invalid={fieldState.invalid} disabled={resetting} className={cn("pr-10", matches && "border-primary/60 bg-primary/5")} />
                  <button type="button" tabIndex={-1} onClick={() => setShowConfirm((p) => !p)} aria-label={showConfirm ? "Hide password" : "Show password"} className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground">
                    {showConfirm ? <IconEyeOff size={16} stroke={1.75} /> : <IconEye size={16} stroke={1.75} />}
                  </button>
                </div>
                {field.value.length > 0 && !fieldState.invalid && (
                  <p className={cn("flex items-center gap-1 text-xs", matches ? "text-primary" : "text-muted-foreground")}>
                    {matches && <IconCheck size={10} stroke={3} />}
                    {matches ? "Passwords match" : mismatch ? "Passwords don't match" : ""}
                  </p>
                )}
                {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
              </Field>
            );
          }} />
        </FieldGroup>
      </div>
      <Button type="submit" size="lg" disabled={isPending} className="w-full">
        {resetting ? <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />Saving password…</> : <><IconCheck size={16} stroke={2} data-icon="inline-start" />Reset password</>}
      </Button>
    </form>
  );
}
