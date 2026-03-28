"use client";

/**
 * TxPanel — look up on-chain transaction status (single or batch).
 *
 * Patterns:
 *  - useTransition for non-urgent async lookups
 *  - Controlled inputs; validation inline
 *  - Batch parses IDs from newline / comma / space (same as WatchPanel)
 *  - Results rendered as a status table with colour-coded badges
 */

import { useState, useTransition } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  IconSearch,
  IconLoader2,
  IconCircleCheck,
  IconClock,
  IconAlertTriangle,
  IconArrowsLeftRight,
  IconBan,
  IconQuestionMark,
  IconReceipt,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import { getTxStatus, getTxStatusBatch } from "@/lib/api/bitcoin";
import type { TxStatus, TxStatusResult } from "@/lib/api/bitcoin";

// ── Status config ─────────────────────────────────────────────────────────────

const STATUS_CFG: Record<
  TxStatus,
  { label: string; Icon: React.ElementType; badge: string; dot: string }
> = {
  confirmed: {
    label: "Confirmed",
    Icon: IconCircleCheck,
    badge:
      "border-green-500/30 bg-green-500/10 text-green-600 dark:text-green-400",
    dot: "bg-green-500",
  },
  mempool: {
    label: "In mempool",
    Icon: IconClock,
    badge:
      "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400",
    dot: "bg-amber-500",
  },
  not_found: {
    label: "Not found",
    Icon: IconQuestionMark,
    badge: "border-border/60 bg-muted/40 text-muted-foreground",
    dot: "bg-muted-foreground/40",
  },
  conflicting: {
    label: "Conflicting",
    Icon: IconArrowsLeftRight,
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
    dot: "bg-destructive",
  },
  abandoned: {
    label: "Abandoned",
    Icon: IconBan,
    badge: "border-border/60 bg-muted/40 text-muted-foreground",
    dot: "bg-muted-foreground/40",
  },
};

// ── TxResultRow ───────────────────────────────────────────────────────────────

function TxResultRow({
  txid,
  result,
}: {
  txid: string;
  result: TxStatusResult;
}) {
  const cfg = STATUS_CFG[result.status];

  return (
    <motion.tr
      initial={{ opacity: 0, y: -4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18, ease: "easeOut" }}
      className="border-b border-border/50 last:border-0 hover:bg-muted/20 transition-colors"
    >
      <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">
        <span title={txid}>{txid.slice(0, 16)}…</span>
      </td>
      <td className="px-4 py-2.5">
        <span
          className={cn(
            "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[11px] font-medium",
            cfg.badge,
          )}
        >
          <span className={cn("size-1.5 rounded-full", cfg.dot)} />
          {cfg.label}
        </span>
      </td>
      <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">
        {result.confirmations !== undefined
          ? result.confirmations.toLocaleString()
          : "—"}
      </td>
      <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">
        {result.block_height !== undefined
          ? result.block_height.toLocaleString()
          : "—"}
      </td>
    </motion.tr>
  );
}

// ── TxPanel ───────────────────────────────────────────────────────────────────

type LookupMode = "single" | "batch";

export function TxPanel() {
  const [mode, setMode] = useState<LookupMode>("single");
  const [singleInput, setSingleInput] = useState("");
  const [batchInput, setBatchInput] = useState("");
  const [results, setResults] = useState<[string, TxStatusResult][] | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [looking, startLookup] = useTransition();

  function parseIds(raw: string): string[] {
    return [
      ...new Set(
        raw
          .split(/[\n,\s]+/)
          .map((id) => id.trim().toLowerCase())
          .filter((id) => id.length === 64 && /^[0-9a-f]+$/.test(id)),
      ),
    ];
  }

  const singleValid =
    singleInput.trim().length === 64 &&
    /^[0-9a-fA-F]+$/.test(singleInput.trim());

  const batchIds = parseIds(batchInput);
  const batchTooMany = batchIds.length > 20;
  const batchValid = batchIds.length > 0 && !batchTooMany;

  function handleLookup() {
    setError(null);
    setResults(null);

    startLookup(async () => {
      try {
        if (mode === "single") {
          const r = await getTxStatus(singleInput.trim());
          setResults([[singleInput.trim().toLowerCase(), r]]);
        } else {
          const r = await getTxStatusBatch(batchIds);
          setResults(Object.entries(r.statuses));
        }
      } catch (err: unknown) {
        setError(
          err instanceof Error ? err.message : "Lookup failed. Try again.",
        );
      }
    });
  }

  const canSubmit = mode === "single" ? singleValid : batchValid;

  return (
    <div className="flex flex-col gap-4 pt-4">
      {/* Mode toggle */}
      <div className="flex items-center gap-3">
        <div className="flex gap-0.5 rounded-lg border border-border bg-muted/40 p-0.5">
          {(["single", "batch"] as LookupMode[]).map((m) => (
            <button
              key={m}
              onClick={() => {
                setMode(m);
                setResults(null);
                setError(null);
              }}
              className={cn(
                "rounded-md px-3 py-1.5 text-xs font-medium capitalize transition-all",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                mode === m
                  ? "bg-background text-foreground shadow-xs"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {m}
            </button>
          ))}
        </div>
        <p className="text-xs text-muted-foreground">
          {mode === "single"
            ? "Look up one transaction by TXID"
            : "Look up up to 20 transactions at once"}
        </p>
      </div>

      {/* Input */}
      <div className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
        {mode === "single" ? (
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-foreground">
              Transaction ID
            </label>
            <input
              type="text"
              value={singleInput}
              onChange={(e) => setSingleInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && canSubmit) handleLookup();
              }}
              placeholder="a1b2c3d4e5f6… (64-char hex)"
              spellCheck={false}
              className={cn(
                "w-full rounded-lg border bg-background px-3 py-2.5",
                "font-mono text-xs text-foreground placeholder:text-muted-foreground/50",
                "transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                singleInput && !singleValid
                  ? "border-destructive/50"
                  : "border-input",
              )}
            />
            {singleInput && !singleValid && (
              <p className="text-[11px] text-destructive">
                Must be a 64-character hex string
              </p>
            )}
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-foreground">
              Transaction IDs
            </label>
            <textarea
              value={batchInput}
              onChange={(e) => setBatchInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && canSubmit)
                  handleLookup();
              }}
              placeholder={"a1b2c3d4… (one per line or comma-separated)"}
              rows={5}
              className={cn(
                "w-full resize-none rounded-lg border bg-background px-3 py-2.5",
                "font-mono text-xs text-foreground placeholder:text-muted-foreground/50",
                "transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                batchTooMany ? "border-destructive/50" : "border-input",
              )}
            />
            <div className="flex items-center justify-between">
              <p
                className={cn(
                  "text-[11px]",
                  batchTooMany
                    ? "text-destructive"
                    : "text-muted-foreground",
                )}
              >
                {batchTooMany
                  ? `Maximum 20 IDs per request (${batchIds.length} detected)`
                  : batchIds.length > 0
                    ? `${batchIds.length} valid ID${batchIds.length !== 1 ? "s" : ""} detected`
                    : "Paste transaction IDs above"}
              </p>
              <span className="text-[10px] text-muted-foreground/60">
                ⌘↵ to search
              </span>
            </div>
          </div>
        )}

        {/* Submit */}
        <button
          onClick={handleLookup}
          disabled={looking || !canSubmit}
          className={cn(
            "flex w-full items-center justify-center gap-2 rounded-lg",
            "border border-primary/40 bg-primary/10 px-4 py-2 text-sm font-medium text-primary",
            "transition-colors hover:bg-primary/20",
            "disabled:cursor-not-allowed disabled:opacity-40",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          )}
        >
          {looking ? (
            <IconLoader2 size={15} stroke={2} className="animate-spin" />
          ) : (
            <IconSearch size={15} stroke={2} />
          )}
          {looking ? "Looking up…" : "Look up"}
        </button>

        {/* Error */}
        <AnimatePresence>
          {error && (
            <motion.div
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
            >
              <IconAlertTriangle size={13} stroke={2} className="shrink-0" />
              {error}
            </motion.div>
          )}
        </AnimatePresence>
      </div>

      {/* Results */}
      <AnimatePresence>
        {results && results.length > 0 && (
          <motion.div
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 4 }}
            className="overflow-hidden rounded-xl border border-border bg-card"
          >
            <div className="flex items-center gap-2 border-b border-border/60 px-4 py-3">
              <IconReceipt
                size={13}
                stroke={1.75}
                className="text-muted-foreground"
              />
              <span className="text-xs font-medium text-foreground">
                Results
              </span>
              <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                {results.length}
              </span>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-border/50 bg-muted/30">
                    {["TXID", "Status", "Confirmations", "Block height"].map(
                      (h) => (
                        <th
                          key={h}
                          className="whitespace-nowrap px-4 py-2 text-left text-[11px] font-medium text-muted-foreground"
                        >
                          {h}
                        </th>
                      ),
                    )}
                  </tr>
                </thead>
                <tbody>
                  {results.map(([txid, result]) => (
                    <TxResultRow key={txid} txid={txid} result={result} />
                  ))}
                </tbody>
              </table>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
