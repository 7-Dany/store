"use client";

import { useEffect, useState } from "react";
import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FieldError, FieldGroup } from "@/components/ui/field";
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSlot,
} from "@/components/ui/input-otp";
import { REGEXP_ONLY_DIGITS } from "input-otp";
import { PasswordStrength } from "./_password-strength";
import { StepDots } from "./_step-dots";
import { useRegister } from "@/features/auth/hooks/use-register";
import { useVerifyEmail } from "@/features/auth/hooks/use-verify-email";
import { useResendVerification } from "@/features/auth/hooks/use-resend-verification";
import {
  IconArrowRight,
  IconArrowLeft,
  IconCheck,
  IconEye,
  IconEyeOff,
  IconLoader2,
  IconMail,
  IconRefresh,
} from "@tabler/icons-react";

const nameSchema = z.object({
  name: z.string().trim().min(1, "Please enter your name.").max(100, "Name must be under 100 characters."),
});

const emailSchema = z.object({
  email: z.string().trim().min(1, "Please enter your email.").email("Please enter a valid email address."),
});

const passwordSchema = z.object({
  password: z
    .string()
    .min(8, "Password must be at least 8 characters.")
    .regex(/[A-Z]/, "Password needs an uppercase letter.")
    .regex(/[a-z]/, "Password needs a lowercase letter.")
    .regex(/[0-9]/, "Password needs a number.")
    .regex(/[^A-Za-z0-9]/, "Password needs a symbol (e.g. !@#$)."),
});

const otpSchema = z.object({
  otp: z.string().length(6, "Enter all 6 digits of the code."),
});

type StepKey = "name" | "email" | "password" | "otp";

const STEPS: {
  key: StepKey;
  heading: string;
  description: (ctx: { name: string; email: string }) => string;
}[] = [
  { key: "name",     heading: "Create your account",  description: () => "What should we call you?" },
  { key: "email",    heading: "What's your email?",   description: ({ name }) => `Nice to meet you${name ? `, ${name.split(" ")[0]}` : ""}! We'll send a verification code here.` },
  { key: "password", heading: "Create a password",    description: () => "Use 8+ characters with uppercase, lowercase, a number, and a symbol." },
  { key: "otp",      heading: "Check your inbox",     description: ({ email }) => `We sent a 6-digit code to ${email || "your email"}.` },
];

export function RegisterForm() {
  const [stepIndex, setStepIndex] = useState(0);
  const [direction, setDirection] = useState<"forward" | "back">("forward");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [showPassword, setShowPassword] = useState(false);

  const step = STEPS[stepIndex];
  const ctx = { name, email };

  const nameForm     = useForm({ resolver: zodResolver(nameSchema),     defaultValues: { name: "" } });
  const emailForm    = useForm({ resolver: zodResolver(emailSchema),    defaultValues: { email: "" } });
  const passwordForm = useForm({ resolver: zodResolver(passwordSchema), defaultValues: { password: "" } });
  const otpForm      = useForm({ resolver: zodResolver(otpSchema),      defaultValues: { otp: "" } });

  const { resend, isPending: resending, countdown } = useResendVerification();

  const { execute: register, isPending: registering } = useRegister({
    onSuccess: () => {
      advance();
      countdown.start(120);
    },
    onEmailTaken: () => {
      goTo(1);
      emailForm.setError("email", { message: "An account with that email already exists." });
    },
    onError: (msg) => passwordForm.setError("password", { message: msg }),
  });

  const { execute: verify, isPending: verifying } = useVerifyEmail({
    onError: (msg) => otpForm.setError("otp", { message: msg }),
  });

  const isPending = registering || verifying || resending;

  function clearAllErrors() {
    nameForm.clearErrors();
    emailForm.clearErrors();
    passwordForm.clearErrors();
    otpForm.clearErrors();
  }

  function advance() { clearAllErrors(); setDirection("forward"); setStepIndex((i) => i + 1); }
  function back()    { clearAllErrors(); setDirection("back");    setStepIndex((i) => Math.max(0, i - 1)); }
  function goTo(i: number) {
    clearAllErrors();
    setDirection(i > stepIndex ? "forward" : "back");
    setStepIndex(i);
  }

  const animClass = direction === "forward"
    ? "animate-in slide-in-from-right-4 fade-in"
    : "animate-in slide-in-from-left-4 fade-in";

  if (step.key === "otp") {
    return (
      <form onSubmit={otpForm.handleSubmit((d) => verify(email, d.otp))} noValidate className="flex flex-col gap-5">
        <div className="grid grid-cols-3 items-center">
          <button type="button" onClick={back} className="-ml-0.5 flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground justify-self-start">
            <IconArrowLeft size={12} stroke={2} /> Back
          </button>
          <StepDots total={STEPS.length} current={stepIndex} className="justify-self-center" />
        </div>
        <div key={stepIndex} className={cn("flex flex-col gap-1.5", animClass)} style={{ animationDuration: "200ms" }}>
          <h2 className="text-base font-semibold text-foreground">{step.heading}</h2>
          <p className="text-sm text-muted-foreground">{step.description(ctx)}</p>
          <div className="mt-3">
            <Controller
              name="otp"
              control={otpForm.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <InputOTP maxLength={6} pattern={REGEXP_ONLY_DIGITS} value={field.value} onChange={(v) => { clearAllErrors(); field.onChange(v); }} disabled={verifying} containerClassName="w-full">
                    <InputOTPGroup className="w-full">
                      {[0,1,2,3,4,5].map((i) => <InputOTPSlot key={i} index={i} aria-invalid={fieldState.invalid} className="flex-1 h-9" />)}
                    </InputOTPGroup>
                  </InputOTP>
                  {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
                </Field>
              )}
            />
            <div className="mt-3 flex items-center justify-center gap-1.5">
              <IconMail size={13} stroke={1.75} className="text-muted-foreground" />
              <span className="text-xs text-muted-foreground">Didn&apos;t receive it?</span>
              {countdown.isActive ? (
                <span className="text-xs text-muted-foreground">Resend in {countdown.formatted}</span>
              ) : (
                <button type="button" onClick={() => { resend(email); otpForm.reset(); clearAllErrors(); }} disabled={resending} className="flex items-center gap-0.5 text-xs text-primary transition-opacity hover:opacity-75 disabled:opacity-50">
                  {resending && <IconRefresh size={11} stroke={2} className="animate-spin" />}
                  Resend code
                </button>
              )}
            </div>
          </div>
        </div>
        <Button type="submit" size="lg" disabled={isPending} className="w-full">
          {verifying ? <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />Verifying…</> : <><IconCheck size={16} stroke={2} data-icon="inline-start" />Verify email</>}
        </Button>
      </form>
    );
  }

  const activeForm = step.key === "name" ? nameForm : step.key === "email" ? emailForm : passwordForm;

  function handleTextSubmit(data: Record<string, string>) {
    if (step.key === "name")       { setName(data.name);   advance(); }
    else if (step.key === "email") { setEmail(data.email); advance(); }
    else { register(name, email, data.password); }
  }

  const fieldName = step.key as "name" | "email" | "password";
  const currentField = activeForm.watch(fieldName as never) as string ?? "";

  return (
    <form onSubmit={activeForm.handleSubmit(handleTextSubmit as never)} noValidate className="flex flex-col gap-5">
      <div className="grid grid-cols-3 items-center">
        {stepIndex > 0 ? (
          <button type="button" onClick={back} className="-ml-0.5 flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground justify-self-start">
            <IconArrowLeft size={12} stroke={2} /> Back
          </button>
        ) : <span />}
        <StepDots total={STEPS.length} current={stepIndex} className="justify-self-center" />
      </div>
      <div key={stepIndex} className={cn("flex flex-col gap-1.5", animClass)} style={{ animationDuration: "200ms" }}>
        <h2 className="text-base font-semibold text-foreground">{step.heading}</h2>
        <p className="text-sm text-muted-foreground">{step.description(ctx)}</p>
        <FieldGroup className="mt-3 gap-0">
          <Controller
            name={fieldName as never}
            control={activeForm.control as never}
            render={({ field, fieldState }: {
              field: { onChange: (e: React.ChangeEvent<HTMLInputElement>) => void; onBlur: () => void; value: string; ref: (el: HTMLInputElement | null) => void; name: string };
              fieldState: { invalid: boolean; error?: { message?: string } };
            }) => (
              <Field data-invalid={fieldState.invalid}>
                <div className="relative">
                  <Input
                    {...field}
                    id={step.key}
                    type={step.key === "password" ? (showPassword ? "text" : "password") : step.key === "email" ? "email" : "text"}
                    autoComplete={step.key === "password" ? "new-password" : step.key === "email" ? "email" : "name"}
                    placeholder={step.key === "name" ? "Your full name" : step.key === "email" ? "you@example.com" : "Password"}
                    aria-invalid={fieldState.invalid}
                    disabled={isPending}
                    onChange={(e) => { clearAllErrors(); field.onChange(e); }}
                    className={step.key === "password" ? "pr-10" : ""}
                  />
                  {step.key === "password" && (
                    <button type="button" tabIndex={-1} onClick={() => setShowPassword((p) => !p)} aria-label={showPassword ? "Hide password" : "Show password"} className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground">
                      {showPassword ? <IconEyeOff size={16} stroke={1.75} /> : <IconEye size={16} stroke={1.75} />}
                    </button>
                  )}
                </div>
                {step.key === "password" && <PasswordStrength value={currentField} />}
                {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
              </Field>
            )}
          />
        </FieldGroup>
      </div>
      <Button type="submit" size="lg" disabled={isPending} className="w-full">
        {isPending ? (
          <><IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />{step.key === "password" ? "Creating account…" : "Loading…"}</>
        ) : (
          <>Continue <IconArrowRight size={16} stroke={2} data-icon="inline-end" /></>
        )}
      </Button>
    </form>
  );
}
