"use client";

import { useState } from "react";
import { Input } from "@/components/ui/input";
import { IconEye, IconEyeOff } from "@tabler/icons-react";

/**
 * Password input with show/hide toggle
 */
export function PasswordInput({
  value,
  onChangeAction,
  placeholder,
  id,
  "aria-invalid": invalid,
  autoComplete,
}: {
  value: string;
  onChangeAction: (v: string) => void;
  placeholder?: string;
  id?: string;
  "aria-invalid"?: boolean;
  autoComplete?: string;
}) {
  const [show, setShow] = useState(false);

  return (
    <div className="relative">
      <Input
        id={id}
        type={show ? "text" : "password"}
        value={value}
        onChange={(e) => onChangeAction(e.target.value)}
        placeholder={placeholder}
        className="pr-9"
        aria-invalid={invalid}
        autoComplete={autoComplete ?? "new-password"}
      />
      <button
        type="button"
        onClick={() => setShow((s) => !s)}
        tabIndex={-1}
        className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
      >
        {show ? <IconEyeOff size={15} stroke={2} /> : <IconEye size={15} stroke={2} />}
      </button>
    </div>
  );
}

/**
 * Labeled form field with error message
 */
export function LabeledField({
  id,
  label,
  error,
  children,
}: {
  id?: string;
  label: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium text-muted-foreground">
        {label}
      </label>
      {children}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

/**
 * Reusable row for displaying field information
 */
export function FieldRow({
  icon,
  label,
  value,
  suffix,
  onEditAction,
  readOnly = false,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  suffix?: React.ReactNode;
  onEditAction?: () => void;
  readOnly?: boolean;
}) {
  return (
    <div className="group/row flex items-center gap-3 py-3">
      <div className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-muted/40 text-muted-foreground">
        {icon}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-sm text-foreground">
            {value || <span className="italic text-muted-foreground">Not set</span>}
          </span>
          {suffix}
        </div>
      </div>
      {!readOnly && onEditAction && (
        <button
          onClick={onEditAction}
          className="shrink-0 gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground opacity-0 transition-all group-hover/row:opacity-100 hover:bg-muted hover:text-foreground"
        >
          Edit
        </button>
      )}
    </div>
  );
}
