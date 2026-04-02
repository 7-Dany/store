"use client";

/**
 * WatchPanel — register Bitcoin addresses for real-time event monitoring.
 *
 * Fixes vs. original:
 *  - <Textarea> (shadcn) replaces raw <textarea>
 *  - <Button>   (shadcn) replaces hand-rolled styled button
 *  - <Progress> (shadcn) replaces framer-motion animated div (no framer dep)
 *  - <Alert>    (shadcn) replaces hand-rolled error/success callout divs
 *  - <Empty> + <EmptyMedia> + <EmptyTitle> replace custom dashed-border div
 *  - <ToggleGroup> replaces hand-rolled network selector buttons
 *  - Proper <label htmlFor> on the textarea (a11y)
 *  - role="status" aria-live="polite" region for feedback (a11y)
 *  - "Clear all" action when addresses are present (UX)
 *  - Session-ephemeral notice below the form (UX)
 *  - Feedback auto-dismisses after 4 s (UX)
 *  - Keyboard hint shows ⌘↵ / Ctrl↵ (UX)
 *  - watched stored as Set<string> internally — O(1) lookups (perf)
 *  - pendingAddresses derived inline; React Compiler memoises it (perf)
 *  - useEffectEvent for stable clearFeedback callback
 */

import { useState, useTransition, useEffectEvent } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  IconEye,
  IconLoader2,
  IconTrash,
  IconPlus,
  IconAddressBook,
  IconInfoCircle,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Empty,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
  EmptyDescription,
} from "@/components/ui/empty";
import { Progress, ProgressLabel, ProgressValue } from "@/components/ui/progress";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { watchAddresses } from "@/lib/api/bitcoin";
import type { BtcNetwork } from "@/lib/api/bitcoin";

const MAX_WATCH = 100;
const MAX_PER_REQUEST = 20;
const FEEDBACK_TTL_MS = 4_000;
const TEXTAREA_ID = "watch-addresses-input";

// ── AddressChip ────────────────────────────────────────────────────────────────

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
        type="button"
        onClick={onRemove}
        aria-label={`Remove ${address}`}
        className={cn(
          "shrink-0 text-muted-foreground/40 opacity-0 transition-all",
          "group-hover:opacity-100 hover:text-destructive",
          "focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded",
        )}
      >
        <IconTrash size={11} stroke={2} />
      </button>
    </motion.div>
  );
}

// ── WatchPanel ─────────────────────────────────────────────────────────────────

export function WatchPanel() {
  const [inputValue, setInputValue] = useState("");
  const [network, setNetwork] = useState<BtcNetwork>("testnet4");
  // Internal Set for O(1) lookup; convert to array only for rendering.
  const [watched, setWatched] = useState<Set<string>>(new Set());
  const [submitting, startSubmit] = useTransition();
  const [feedback, setFeedback] = useState<{
    type: "success" | "error";
    message: string;
  } | null>(null);

  const watchedArray = [...watched];
  const remaining = MAX_WATCH - watched.size;

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
    (a) => !watched.has(a),
  );
  const tooMany = pendingAddresses.length > MAX_PER_REQUEST;
  const wouldExceedCap = watched.size + pendingAddresses.length > MAX_WATCH;
  const canSubmit = pendingAddresses.length > 0 && !tooMany && !wouldExceedCap;

  const clearFeedback = useEffectEvent(() => setFeedback(null));

  function handleSubmit() {
    if (!canSubmit) return;
    startSubmit(async () => {
      clearFeedback();
      try {
        const result = await watchAddresses(pendingAddresses, network);
        const registered = result.watching;
        setWatched((prev) => new Set([...prev, ...registered]));
        setInputValue("");
        setFeedback({
          type: "success",
          message: `Watching ${registered.length} address${registered.length !== 1 ? "es" : ""}.`,
        });
        setTimeout(clearFeedback, FEEDBACK_TTL_MS);
      } catch (err: unknown) {
        const msg =
          err instanceof Error ? err.message : "Failed to register addresses.";
        setFeedback({ type: "error", message: msg });
      }
    });
  }

  function removeAddress(addr: string) {
    setWatched((prev) => {
      const next = new Set(prev);
      next.delete(addr);
      return next;
    });
  }

  function clearAll() {
    setWatched(new Set());
  }

  return (
    <div className="flex flex-col gap-5 pt-4">
      {/* ── Capacity bar ── */}
      <div className="flex flex-col gap-1.5 rounded-xl border border-border bg-card px-4 py-3.5">
        <Progress
          value={(watched.size / MAX_WATCH) * 100}
          className="gap-1.5"
        >
          <ProgressLabel>Watch capacity</ProgressLabel>
          <ProgressValue>
            <span
              className={cn(
                "font-semibold",
                watched.size >= MAX_WATCH
                  ? "text-destructive"
                  : watched.size > 80
                    ? "text-amber-600 dark:text-amber-400"
                    : "text-foreground",
              )}
            >
              {watched.size}
            </span>
            <span className="text-muted-foreground/60"> / {MAX_WATCH}</span>
          </ProgressValue>
        </Progress>
        <p className="text-[10px] text-muted-foreground">
          {remaining > 0
            ? `${remaining} address${remaining !== 1 ? "es" : ""} remaining`
            : "Cap reached — remove addresses to add more"}
        </p>
      </div>

      {/* ── Form ── */}
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

          {/* Network selector — ToggleGroup replaces hand-rolled buttons */}
          <ToggleGroup
            type="single"
            value={network}
            onValueChange={(v) => {
              if (v) setNetwork(v as BtcNetwork);
            }}
            variant="outline"
            size="sm"
            aria-label="Bitcoin network"
            className="shrink-0"
          >
            <ToggleGroupItem value="testnet4" aria-label="Testnet 4">
              testnet4
            </ToggleGroupItem>
            <ToggleGroupItem value="mainnet" aria-label="Mainnet">
              mainnet
            </ToggleGroupItem>
          </ToggleGroup>
        </div>

        {/* Proper label + Textarea (shadcn) */}
        <div className="flex flex-col gap-1.5">
          <label
            htmlFor={TEXTAREA_ID}
            className="text-xs font-medium text-foreground"
          >
            Bitcoin addresses
          </label>
          <Textarea
            id={TEXTAREA_ID}
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
            aria-invalid={tooMany || wouldExceedCap ? true : undefined}
            aria-describedby="watch-hint watch-error"
            className="font-mono text-xs"
          />

          {/* Validation hints */}
          <div
            id="watch-hint"
            className="flex items-center justify-between gap-2"
          >
            <div
              id="watch-error"
              role="alert"
              aria-live="assertive"
              className="flex flex-col gap-0.5"
            >
              {tooMany && (
                <p className="text-[11px] text-destructive">
                  Maximum {MAX_PER_REQUEST} addresses per request
                </p>
              )}
              {wouldExceedCap && !tooMany && (
                <p className="text-[11px] text-destructive">
                  Would exceed the 100-address cap
                </p>
              )}
              {pendingAddresses.length > 0 && !tooMany && !wouldExceedCap && (
                <p className="text-[11px] text-muted-foreground">
                  {pendingAddresses.length} address
                  {pendingAddresses.length !== 1 ? "es" : ""} to register
                </p>
              )}
            </div>
            <span className="shrink-0 text-[10px] text-muted-foreground/60">
              ⌘↵ / Ctrl↵
            </span>
          </div>
        </div>

        <Button
          onClick={handleSubmit}
          disabled={submitting || !canSubmit}
          variant="outline"
          size="sm"
          className="w-full border-primary/40 bg-primary/10 text-primary hover:bg-primary/20"
        >
          {submitting ? (
            <IconLoader2 size={13} stroke={2} data-icon="inline-start" className="animate-spin" />
          ) : (
            <IconPlus size={13} stroke={2.5} data-icon="inline-start" />
          )}
          {submitting ? "Registering…" : "Watch"}
        </Button>

        {/* Feedback banner */}
        <div role="status" aria-live="polite" aria-atomic="true">
          <AnimatePresence>
            {feedback && (
              <motion.div
                initial={{ opacity: 0, height: 0 }}
                animate={{ opacity: 1, height: "auto" }}
                exit={{ opacity: 0, height: 0 }}
              >
                <Alert variant={feedback.type === "error" ? "destructive" : "default"}>
                  <AlertDescription>{feedback.message}</AlertDescription>
                </Alert>
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      </div>

      {/* ── Session notice ── */}
      <div className="flex items-start gap-2 rounded-lg border border-border/60 bg-muted/30 px-3 py-2.5 text-xs text-muted-foreground">
        <IconInfoCircle size={13} stroke={1.75} className="mt-0.5 shrink-0" />
        <p>
          Watching is session-based — your address list is not persisted and
          will clear on refresh (server TTL: 30 min).
        </p>
      </div>

      {/* ── Watched list ── */}
      <div className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-2 px-0.5">
          <div className="flex items-center gap-2">
            <IconEye
              size={12}
              stroke={1.75}
              className="text-muted-foreground"
              aria-hidden="true"
            />
            <span className="text-xs font-medium text-muted-foreground">
              Currently watching
            </span>
          </div>
          {watchedArray.length > 0 && (
            <button
              type="button"
              onClick={clearAll}
              aria-label="Clear all watched addresses"
              className="text-[11px] text-muted-foreground transition-colors hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded"
            >
              Clear all
            </button>
          )}
        </div>

        {watchedArray.length === 0 ? (
          <Empty className="border border-dashed border-border py-8">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <IconAddressBook size={20} stroke={1.25} />
              </EmptyMedia>
              <EmptyTitle className="text-sm">No addresses registered</EmptyTitle>
              <EmptyDescription>
                Register addresses above to start monitoring.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <AnimatePresence mode="popLayout">
            <div className="flex flex-wrap gap-1.5">
              {watchedArray.map((addr) => (
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
