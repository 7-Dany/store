"use client";

import * as React from "react";
import { Toaster as SonnerPrimitive, toast as sonnerToast } from "sonner";
import type { ExternalToast, ToasterProps } from "sonner";
import { motion } from "framer-motion";
import { useTheme } from "next-themes";
import {
  IconCircleCheck,
  IconCircleX,
  IconAlertTriangle,
  IconInfoCircle,
  IconLoader2,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";

// ─── Variant config ───────────────────────────────────────────────────────────

type ToastVariant = "default" | "success" | "error" | "warning" | "info";

const VARIANT: Record<
  ToastVariant,
  { Icon: React.ElementType; fill: string; border: string; fg: string }
> = {
  default: {
    Icon: IconInfoCircle,
    fill: "--toast-default-fill",
    border: "--toast-default-border",
    fg: "--toast-default-fg",
  },
  success: {
    Icon: IconCircleCheck,
    fill: "--toast-success-fill",
    border: "--toast-success-border",
    fg: "--toast-success-fg",
  },
  error: {
    Icon: IconCircleX,
    fill: "--toast-error-fill",
    border: "--toast-error-border",
    fg: "--toast-error-fg",
  },
  warning: {
    Icon: IconAlertTriangle,
    fill: "--toast-warning-fill",
    border: "--toast-warning-border",
    fg: "--toast-warning-fg",
  },
  info: {
    Icon: IconInfoCircle,
    fill: "--toast-info-fill",
    border: "--toast-info-border",
    fg: "--toast-info-fg",
  },
};

// ─── Border radius ────────────────────────────────────────────────────────────
// Pill and organic-expanded values (per-corner for smooth framer-motion morph).

const PILL = { tl: 9999, tr: 9999, br: 9999, bl: 9999 };
const BLOB = { tl: 18,   tr: 22,   br: 20,   bl: 16   };

// ─── PillToast ────────────────────────────────────────────────────────────────

interface PillToastProps {
  id: string | number;
  title: string;
  description?: string;
  variant: ToastVariant;
}

function PillToast({ title, description, variant }: PillToastProps) {
  const { Icon, fill, border, fg } = VARIANT[variant];
  const expanded = !!description;
  const r = expanded ? BLOB : PILL;

  return (
    <motion.div
      // Layout changes (e.g. text reflow) animate smoothly
      layout
      initial={{
        opacity: 0,
        y: 10,
        scale: 0.88,
        borderTopLeftRadius: PILL.tl,
        borderTopRightRadius: PILL.tr,
        borderBottomRightRadius: PILL.br,
        borderBottomLeftRadius: PILL.bl,
      }}
      animate={{
        opacity: 1,
        y: 0,
        scale: 1,
        borderTopLeftRadius: r.tl,
        borderTopRightRadius: r.tr,
        borderBottomRightRadius: r.br,
        borderBottomLeftRadius: r.bl,
      }}
      transition={{
        // Spring for shape + position
        type: "spring",
        stiffness: 420,
        damping: 32,
        // Linear tween for opacity so it doesn't overshoot
        opacity: { type: "tween", duration: 0.18, ease: "easeOut" },
      }}
      style={{
        background: `var(${fill})`,
        borderWidth: "1.5px",
        borderStyle: "solid",
        borderColor: `var(${border})`,
        color: `var(${fg})`,
        fontFamily: "var(--font-sans, -apple-system, BlinkMacSystemFont, sans-serif)",
        boxShadow:
          "0 4px 18px rgba(0,0,0,0.07), 0 1px 4px rgba(0,0,0,0.04)",
      }}
      className={cn(
        "pointer-events-auto relative w-fit",
        expanded
          ? "min-w-65 max-w-90 px-3.5 pb-4 pt-2.5"
          : "px-3 py-2",
      )}
    >
      {/* ── Header row: icon + title ── */}
      <div className="flex items-center gap-2">
        <Icon
          size={13}
          stroke={2.25}
          className="shrink-0"
          aria-hidden
        />
        <span className="text-[12px] font-bold leading-none whitespace-nowrap">
          {title}
        </span>
      </div>

      {/* ── Description ── */}
      {expanded && (
        <motion.p
          initial={{ opacity: 0 }}
          animate={{ opacity: 0.78 }}
          transition={{ type: "tween", duration: 0.22, delay: 0.06 }}
          className="mt-2.5 text-[12.5px] leading-relaxed"
        >
          {description}
        </motion.p>
      )}
    </motion.div>
  );
}

// ─── Toaster ──────────────────────────────────────────────────────────────────

function Toaster({ ...props }: ToasterProps) {
  const { theme = "system" } = useTheme();

  return (
    <SonnerPrimitive
      theme={theme as ToasterProps["theme"]}
      position="bottom-right"
      gap={8}
      offset={20}
      // Visual styling is fully handled by PillToast — sonner only manages
      // stacking, positioning, and queue logic.
      toastOptions={{
        unstyled: true,
        className: "!p-0",
      }}
      {...props}
    />
  );
}

// ─── Public toast API ─────────────────────────────────────────────────────────

export interface ToastOptions {
  description?: string;
  duration?: number;
  id?: string | number;
}

type ToastFn = (title: string, options?: ToastOptions) => string | number;

function make(variant: ToastVariant): ToastFn {
  return (title, options) =>
    sonnerToast.custom(
      (id) => (
        <PillToast
          id={id}
          title={title}
          description={options?.description}
          variant={variant}
        />
      ),
      {
        duration: options?.duration ?? 4000,
        id: options?.id,
        unstyled: true,
        className: "!p-0",
      } satisfies ExternalToast,
    );
}

/**
 * Drop-in toast API.
 *
 * @example
 * toast.success("Saved!")
 * toast.error("Something went wrong", { description: "Try again later." })
 * toast.dismiss()
 */
export const toast: ToastFn & {
  success: ToastFn;
  error:   ToastFn;
  warning: ToastFn;
  info:    ToastFn;
  dismiss: typeof sonnerToast.dismiss;
  promise: typeof sonnerToast.promise;
  loading: (title: string, options?: ToastOptions) => string | number;
} = Object.assign(make("default"), {
  success: make("success"),
  error:   make("error"),
  warning: make("warning"),
  info:    make("info"),
  loading: (title: string, options?: ToastOptions) =>
    sonnerToast.loading(title, {
      duration: options?.duration,
      id: options?.id,
    }),
  dismiss: sonnerToast.dismiss,
  promise: sonnerToast.promise,
});

export { Toaster };
export type { ToasterProps };
