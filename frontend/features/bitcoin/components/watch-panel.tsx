"use client";

/**
 * WatchPanel — register Bitcoin addresses for real-time event monitoring.
 *
 * Patterns used:
 *  - useTransition for non-urgent async submit
 *  - useEffectEvent for stable form-reset callback
 *  - Controlled textarea; addresses parsed from newline / comma / space
 *  - Watched set is client-side only (server is ephemeral Redis, 30 min TTL)
 */

import { useState, useTransition, useEffectEvent } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  IconEye,
  IconLoader2,
  IconCircleCheck,
  IconAlertTriangle,
  IconTrash,
  IconPlus,
  IconAddressBook,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import { watchAddresses } from "@/lib/api/bitcoin";
import type { BtcNetwork } from "@/lib/api/bitcoin";

const MAX_WATCH = 100;
const MAX_PER_REQUEST = 20;

// ── AddressChip ───────────────────────────────────────────────────────────────

function AddressChip({
  address,
  onRemove,
}: {
  address: string;
  onRemove: () => void;
}) {
  return (
    <motion.div
      initial={{ opacity: 0, scale: 0.94 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.9 }}
      transition={{ duration: 0.14 }}
      className="group flex items-center gap-1.5 rounded-lg border border-border bg-muted/40 px-2.5 py-1.5"
    >
      <span className="max-w-[180px] truncate font-mono text-[11px] text-foreground">
        {address}
      </span>
      <button
        onClick={onRemove}
        aria-label={`Remove ${address}`}
        className={cn(
          "shrink-0 text-muted-foreground/40 opacity-0 transition-all",
          "group-hover:opacity-100 hover:text-destructive",
          "focus-visible:opacity-100 focus-visible:outline-none",
        )}
      >
        <IconTrash size={11} stroke={2} />
      </button>
    </motion.div>
  );
}

// ── WatchPanel ────────────────────────────────────────────────────────────────

interface WatchPanelProps {
  onWatched?: (addresses: string[]) => void;
}

export function WatchPanel({ onWatched }: WatchPanelProps) {
  const [inputValue, setInputValue] = useState("");
  const [network, setNetwork] = useState<BtcNetwork>("testnet4");
  const [watched, setWatched] = useState<string[]>([]);
  const [submitting, startSubmit] = useTransition();
  const [feedback, setFeedback] = useState<{
    type: "success" | "error";
    message: string;
  } | null>(null);

  const remaining = MAX_WATCH - watched.length;

  // Parse raw input into a clean deduped list of addresses
  function parseInput(raw: string): string[] {
    return [
      ...new Set(
        raw
          .split(/[\n,\s]+/)
          .map((a) => a.trim())
          .filter(Boolean),
      ),
    ];
  }

  const pendingAddresses = parseInput(inputValue).filter(
    (a) => !watched.includes(a),
  );
  const tooMany = pendingAddresses.length > MAX_PER_REQUEST;
  const wouldExceedCap = watched.length + pendingAddresses.length > MAX_WATCH;

  const clearFeedback = useEffectEvent(() => {
    setFeedback(null);
  });

  function handleSubmit() {
    if (pendingAddresses.length === 0 || tooMany || wouldExceedCap) return;

    startSubmit(async () => {
      clearFeedback();
      try {
        const result = await watchAddresses(pendingAddresses, network);
        const registered = result.watching;
        setWatched((prev) => [...new Set([...prev, ...registered])]);
        setInputValue("");
        setFeedback({
          type: "success",
          message: `Watching ${registered.length} address${registered.length !== 1 ? "es" : ""}.`,
        });
        onWatched?.(registered);
      } catch (err: unknown) {
        const msg =
          err instanceof Error ? err.message : "Failed to register addresses.";
        setFeedback({ type: "error", message: msg });
      }
    });
  }

  function removeAddress(addr: string) {
    setWatched((prev) => prev.filter((a) => a !== addr));
  }

  return (
    <div className="flex flex-col gap-5 pt-4">
      {/* Capacity bar */}
      <div className="flex flex-col gap-1.5 rounded-xl border border-border bg-card px-4 py-3.5">
        <div className="flex items-center justify-between">
          <span className="text-xs font-medium text-foreground">
            Watch capacity
          </span>
          <span className="font-mono text-xs text-muted-foreground">
            <span
              className={cn(
                "font-semibold",
                watched.length >= MAX_WATCH
                  ? "text-destructive"
                  : watched.length > 80
                    ? "text-amber-600 dark:text-amber-400"
                    : "text-foreground",
              )}
            >
              {watched.length}
            </span>
            <span className="text-muted-foreground/60"> / {MAX_WATCH}</span>
          </span>
        </div>
        <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <motion.div
            className={cn(
              "h-full rounded-full transition-colors",
              watched.length >= MAX_WATCH
                ? "bg-destructive"
                : watched.length > 80
                  ? "bg-amber-500"
                  : "bg-primary",
            )}
            animate={{ width: `${(watched.length / MAX_WATCH) * 100}%` }}
            transition={{ duration: 0.3, ease: "easeOut" }}
          />
        </div>
        <p className="text-[10px] text-muted-foreground">
          {remaining > 0
            ? `${remaining} address${remaining !== 1 ? "es" : ""} remaining`
            : "Cap reached — remove addresses to add more"}
        </p>
      </div>

      {/* Form */}
      <div className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
        <div className="flex items-center justify-between gap-3">
          <div>
            <p className="text-sm font-medium text-foreground">
              Register addresses
            </p>
            <p className="text-xs text-muted-foreground">
              Paste up to {MAX_PER_REQUEST} addresses — one per line, comma, or
              space separated
            </p>
          </div>

          {/* Network selector */}
          <div className="flex shrink-0 gap-0.5 rounded-lg border border-border bg-muted/40 p-0.5">
            {(["testnet4", "mainnet"] as BtcNetwork[]).map((n) => (
              <button
                key={n}
                onClick={() => setNetwork(n)}
                className={cn(
                  "rounded-md px-2.5 py-1 text-[11px] font-medium transition-all",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  network === n
                    ? "bg-background text-foreground shadow-xs"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {n}
              </button>
            ))}
          </div>
        </div>

        <textarea
          value={inputValue}
          onChange={(e) => setInputValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) handleSubmit();
          }}
          placeholder={
            network === "testnet4"
              ? "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx"
              : "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
          }
          rows={4}
          className={cn(
            "w-full resize-none rounded-lg border bg-background px-3 py-2.5",
            "font-mono text-xs text-foreground placeholder:text-muted-foreground/50",
            "transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            tooMany || wouldExceedCap
              ? "border-destructive/50"
              : "border-input",
          )}
        />

        {/* Validation hints */}
        <div className="flex items-center justify-between gap-2">
          <div className="flex flex-col gap-0.5">
            {tooMany && (
              <p className="text-[11px] text-destructive">
                Maximum {MAX_PER_REQUEST} addresses per request
              </p>
            )}
            {wouldExceedCap && !tooMany && (
              <p className="text-[11px] text-destructive">
                Would exceed 100-address cap
              </p>
            )}
            {pendingAddresses.length > 0 && !tooMany && !wouldExceedCap && (
              <p className="text-[11px] text-muted-foreground">
                {pendingAddresses.length} address
                {pendingAddresses.length !== 1 ? "es" : ""} to register
              </p>
            )}
          </div>
          <button
            onClick={handleSubmit}
            disabled={
              submitting ||
              pendingAddresses.length === 0 ||
              tooMany ||
              wouldExceedCap
            }
            className={cn(
              "flex shrink-0 items-center gap-1.5 rounded-lg border border-primary/40 bg-primary/10",
              "px-3 py-1.5 text-xs font-medium text-primary",
              "transition-colors hover:bg-primary/20",
              "disabled:cursor-not-allowed disabled:opacity-40",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            )}
          >
            {submitting ? (
              <IconLoader2 size={13} stroke={2} className="animate-spin" />
            ) : (
              <IconPlus size={13} stroke={2.5} />
            )}
            Watch
          </button>
        </div>

        {/* Feedback */}
        <AnimatePresence>
          {feedback && (
            <motion.div
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className={cn(
                "flex items-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium",
                feedback.type === "success"
                  ? "border-green-500/30 bg-green-500/10 text-green-600 dark:text-green-400"
                  : "border-destructive/30 bg-destructive/10 text-destructive",
              )}
            >
              {feedback.type === "success" ? (
                <IconCircleCheck size={13} stroke={2} className="shrink-0" />
              ) : (
                <IconAlertTriangle size={13} stroke={2} className="shrink-0" />
              )}
              {feedback.message}
            </motion.div>
          )}
        </AnimatePresence>
      </div>

      {/* Watched list */}
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2 px-0.5">
          <IconEye size={12} stroke={1.75} className="text-muted-foreground" />
          <span className="text-xs font-medium text-muted-foreground">
            Currently watching
          </span>
        </div>

        {watched.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed border-border py-8 text-center text-muted-foreground">
            <IconAddressBook
              size={22}
              stroke={1.25}
              className="text-muted-foreground/30"
            />
            <p className="text-xs">No addresses registered yet</p>
          </div>
        ) : (
          <AnimatePresence mode="popLayout">
            <div className="flex flex-wrap gap-1.5">
              {watched.map((addr) => (
                <AddressChip
                  key={addr}
                  address={addr}
                  onRemove={() => removeAddress(addr)}
                />
              ))}
            </div>
          </AnimatePresence>
        )}
      </div>
    </div>
  );
}
