"use client";

import { useRef, useEffect, useState } from "react";
import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FieldError, FieldGroup } from "@/components/ui/field";
import { StepDots } from "./_step-dots";
import { useLogin } from "@/features/auth/hooks/use-login";
import {
  IconArrowRight,
  IconArrowLeft,
  IconEye,
  IconEyeOff,
  IconLoader2,
} from "@tabler/icons-react";

const identifierSchema = z.object({
  identifier: z
    .string()
    .min(1, "Please enter your email or username.")
    .max(254, "Identifier is too long."),
});

const passwordSchema = z.object({
  password: z.string().min(1, "Please enter your password."),
});

type IdentifierData = z.infer<typeof identifierSchema>;
type PasswordData = z.infer<typeof passwordSchema>;

interface LoginFormProps {
  initialIdentifier?: string;
}

export function LoginForm({ initialIdentifier = "" }: LoginFormProps) {
  const [stepIndex, setStepIndex] = useState(initialIdentifier ? 1 : 0);
  const [direction, setDirection] = useState<"forward" | "back">("forward");
  const [identifier, setIdentifier] = useState(initialIdentifier);
  const [showPassword, setShowPassword] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const identifierForm = useForm<IdentifierData>({
    resolver: zodResolver(identifierSchema),
    defaultValues: { identifier: initialIdentifier },
  });

  const passwordForm = useForm<PasswordData>({
    resolver: zodResolver(passwordSchema),
    defaultValues: { password: "" },
  });

  const { execute, isPending } = useLogin({
    onError: (msg) => passwordForm.setError("password", { message: msg }),
  });

  useEffect(() => {
    const id = setTimeout(() => inputRef.current?.focus(), 30);
    return () => clearTimeout(id);
  }, [stepIndex]);

  function advance() { setDirection("forward"); setStepIndex(1); }

  function back() {
    passwordForm.reset();
    setDirection("back");
    setStepIndex(0);
  }

  function onIdentifierSubmit(data: IdentifierData) {
    setIdentifier(data.identifier.trim());
    advance();
  }

  function onPasswordSubmit(data: PasswordData) {
    execute(identifier, data.password);
  }

  const animClass =
    direction === "forward"
      ? "animate-in slide-in-from-right-4 fade-in"
      : "animate-in slide-in-from-left-4 fade-in";

  if (stepIndex === 0) {
    return (
      <form
        onSubmit={identifierForm.handleSubmit(onIdentifierSubmit)}
        noValidate
        className="flex flex-col gap-5"
      >
        <div className="grid grid-cols-3 items-center">
          <span />
          <StepDots total={2} current={0} className="justify-self-center" />
        </div>
        <div
          key="identifier"
          className={cn("flex flex-col gap-1.5", animClass)}
          style={{ animationDuration: "200ms" }}
        >
          <h2 className="text-base font-semibold text-foreground">Welcome back</h2>
          <p className="text-sm text-muted-foreground">
            Enter your email address or username to continue.
          </p>
          <FieldGroup className="mt-3 gap-0">
            <Controller
              name="identifier"
              control={identifierForm.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <Input
                    {...field}
                    ref={(el) => {
                      field.ref(el);
                      (inputRef as React.RefObject<HTMLInputElement | null>).current = el;
                    }}
                    id="identifier"
                    type="text"
                    autoComplete="username email"
                    placeholder="Email or username"
                    aria-invalid={fieldState.invalid}
                    disabled={isPending}
                  />
                  {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
                </Field>
              )}
            />
          </FieldGroup>
        </div>
        <Button type="submit" size="lg" disabled={isPending} className="w-full">
          Continue
          <IconArrowRight size={16} stroke={2} data-icon="inline-end" />
        </Button>
      </form>
    );
  }

  return (
    <form
      onSubmit={passwordForm.handleSubmit(onPasswordSubmit)}
      noValidate
      className="flex flex-col gap-5"
    >
      <div className="grid grid-cols-3 items-center">
        <button
          type="button"
          onClick={back}
          className="-ml-0.5 flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground justify-self-start"
        >
          <IconArrowLeft size={12} stroke={2} />
          Back
        </button>
        <StepDots total={2} current={1} className="justify-self-center" />
      </div>
      <div
        key="password"
        className={cn("flex flex-col gap-1.5", animClass)}
        style={{ animationDuration: "200ms" }}
      >
        <h2 className="text-base font-semibold text-foreground">Enter your password</h2>
        <p className="text-sm text-muted-foreground">
          Enter the password associated with your account.
        </p>
        <div className="mt-0.5 flex items-center justify-between gap-3">
          <span className="truncate text-sm font-medium text-foreground" title={identifier}>
            {identifier}
          </span>
          <a
            href={`/forgot-password?email=${encodeURIComponent(identifier.trim())}`}
            className="shrink-0 text-xs text-muted-foreground transition-colors hover:text-foreground"
          >
            Forgot password?
          </a>
        </div>
        <FieldGroup className="mt-3 gap-0">
          <Controller
            name="password"
            control={passwordForm.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <div className="relative">
                  <Input
                    {...field}
                    ref={(el) => {
                      field.ref(el);
                      (inputRef as React.RefObject<HTMLInputElement | null>).current = el;
                    }}
                    id="password"
                    type={showPassword ? "text" : "password"}
                    autoComplete="current-password"
                    placeholder="Password"
                    aria-invalid={fieldState.invalid}
                    disabled={isPending}
                    onChange={(e) => {
                      passwordForm.clearErrors("password");
                      field.onChange(e);
                    }}
                    className="pr-10"
                  />
                  <button
                    type="button"
                    tabIndex={-1}
                    onClick={() => setShowPassword((p) => !p)}
                    aria-label={showPassword ? "Hide password" : "Show password"}
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
                  >
                    {showPassword ? <IconEyeOff size={16} stroke={1.75} /> : <IconEye size={16} stroke={1.75} />}
                  </button>
                </div>
                {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
              </Field>
            )}
          />
        </FieldGroup>
      </div>
      <Button type="submit" size="lg" disabled={isPending} className="w-full">
        {isPending ? (
          <>
            <IconLoader2 size={16} stroke={2} className="animate-spin" data-icon="inline-start" />
            Signing in…
          </>
        ) : (
          "Sign in"
        )}
      </Button>
    </form>
  );
}
