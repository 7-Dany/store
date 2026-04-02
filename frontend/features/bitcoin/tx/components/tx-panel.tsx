"use client";

/**
 * TxPanel — look up on-chain transaction status (single or batch).
 *
 * Fixes vs. original:
 *  - <Input>       (shadcn) replaces raw <input>
 *  - <Textarea>    (shadcn) replaces raw <textarea>
 *  - <Button>      (shadcn) replaces hand-rolled styled button
 *  - <Alert>       (shadcn) replaces hand-rolled error div
 *  - <Table> family (shadcn) replaces raw <table>
 *  - <ToggleGroup> (shadcn) replaces hand-rolled mode selector buttons
 *  - Proper <label htmlFor> on every input (a11y)
 *  - role="alert" aria-live on error (a11y — handled by <Alert>)
 *  - Table has aria-label (a11y)
 *  - singleInput normalised to lowercase before submission (correctness)
 *  - Keyboard shortcut hint shows ⌘↵ / Ctrl↵ (UX)
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
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { getTxStatus, getTxStatusBatch } from "@/lib/api/bitcoin";
import type { TxStatus, TxStatusResult } from "@/lib/api/bitcoin";

// ── Status config ──────────────────────────────────────────────────────────────

const STATUS_CFG: Record<
  TxStatus,
  { label: string; Icon: React.ElementType; badge: string; dot: string }
> = {
  confirmed: {
    label: "Confirmed",
    Icon: IconCircleCheck,
    badge: "border-green-500/30 bg-green-500/10 text-green-600 dark:text-green-400",
    dot: "bg-green-500",
  },
  mempool: {
    label: "In mempool",
    Icon: IconClock,
    badge: "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400",
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

// ── TxResultRow ────────────────────────────────────────────────────────────────

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
      {/* shadcn TableRow / TableCell aren't used here because we need the
          framer-motion `as="tr"` — the cells still follow the same pattern. */}
      <TableCell className="font-mono text-xs text-muted-foreground">
        <span title={txid}>{txid.slice(0, 16)}…</span>
      </TableCell>
      <TableCell>
        <span
          className={cn(
            "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[11px] font-medium",
            cfg.badge,
          )}
        >
          <span className={cn("size-1.5 rounded-full", cfg.dot)} aria-hidden="true" />
          {cfg.label}
        </span>
      </TableCell>
      <TableCell className="font-mono text-xs text-muted-foreground">
        {result.confirmations !== undefined
          ? result.confirmations.toLocaleString()
          : "—"}
      </TableCell>
      <TableCell className="font-mono text-xs text-muted-foreground">
        {result.block_height !== undefined
          ? result.block_height.toLocaleString()
          : "—"}
      </TableCell>
    </motion.tr>
  );
}

// ── TxPanel ────────────────────────────────────────────────────────────────────

type LookupMode = "single" | "batch";

const SINGLE_INPUT_ID = "tx-single-input";
const BATCH_INPUT_ID = "tx-batch-input";

export function TxPanel() {
  const [mode, setMode] = useState<LookupMode>("single");
  const [singleInput, setSingleInput] = useState("");
  const [batchInput, setBatchInput] = useState("");
  const [results, setResults] = useState<[string, TxStatusResult][] | null>(null);
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

  // Normalise to lowercase before validation — fixes case-sensitivity bug.
  const singleNorm = singleInput.trim().toLowerCase();
  const singleValid =
    singleNorm.length === 64 && /^[0-9a-f]+$/.test(singleNorm);

  const batchIds = parseIds(batchInput);
  const batchTooMany = batchIds.length > 20;
  const batchValid = batchIds.length > 0 && !batchTooMany;
  const canSubmit = mode === "single" ? singleValid : batchValid;

  function handleLookup() {
    setError(null);
    setResults(null);

    startLookup(async () => {
      try {
        if (mode === "single") {
          const r = await getTxStatus(singleNorm);
          setResults([[singleNorm, r]]);
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

  return (
    <div className="flex flex-col gap-4 pt-4">
      {/* ── Mode toggle — ToggleGroup replaces hand-rolled buttons ── */}
      <div className="flex items-center gap-3">
        <ToggleGroup
          type="single"
          value={mode}
          onValueChange={(v) => {
            if (!v) return;
            setMode(v as LookupMode);
            setResults(null);
            setError(null);
          }}
          variant="outline"
          size="sm"
          aria-label="Lookup mode"
        >
          <ToggleGroupItem value="single" aria-label="Single transaction">
            Single
          </ToggleGroupItem>
          <ToggleGroupItem value="batch" aria-label="Batch transactions">
            Batch
          </ToggleGroupItem>
        </ToggleGroup>
        <p className="text-xs text-muted-foreground">
          {mode === "single"
            ? "Look up one transaction by TXID"
            : "Look up up to 20 transactions at once"}
        </p>
      </div>

      {/* ── Input card ── */}
      <div className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
        {mode === "single" ? (
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor={SINGLE_INPUT_ID}
              className="text-xs font-medium text-foreground"
            >
              Transaction ID
            </label>
            <Input
              id={SINGLE_INPUT_ID}
              type="text"
              value={singleInput}
              onChange={(e) => setSingleInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && canSubmit) handleLookup();
              }}
              placeholder="a1b2c3d4e5f6… (64-char hex)"
              spellCheck={false}
              aria-invalid={singleInput && !singleValid ? true : undefined}
              aria-describedby="single-error"
              className="font-mono text-xs"
            />
            <div
              id="single-error"
              role="alert"
              aria-live="assertive"
              className="min-h-[1rem]"
            >
              {singleInput && !singleValid && (
                <p className="text-[11px] text-destructive">
                  Must be a 64-character hex string
                </p>
              )}
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor={BATCH_INPUT_ID}
              className="text-xs font-medium text-foreground"
            >
              Transaction IDs
            </label>
            <Textarea
              id={BATCH_INPUT_ID}
              value={batchInput}
              onChange={(e) => setBatchInput(e.target.value)}
              onKeyDown={(e) => {
                if (
                  e.key === "Enter" &&
                  (e.metaKey || e.ctrlKey) &&
                  canSubmit
                )
                  handleLookup();
              }}
              placeholder="a1b2c3d4… (one per line or comma-separated)"
              rows={5}
              aria-invalid={batchTooMany ? true : undefined}
              aria-describedby="batch-hint batch-error"
              className="font-mono text-xs"
            />
            <div className="flex items-center justify-between gap-2">
              <div
                id="batch-error"
                role="alert"
                aria-live="assertive"
              >
                {batchTooMany ? (
                  <p className="text-[11px] text-destructive">
                    Maximum 20 IDs per request ({batchIds.length} detected)
                  </p>
                ) : batchIds.length > 0 ? (
                  <p
                    id="batch-hint"
                    className="text-[11px] text-muted-foreground"
                  >
                    {batchIds.length} valid ID
                    {batchIds.length !== 1 ? "s" : ""} detected
                  </p>
                ) : (
                  <p
                    id="batch-hint"
                    className="text-[11px] text-muted-foreground"
                  >
                    Paste transaction IDs above
                  </p>
                )}
              </div>
              <span className="shrink-0 text-[10px] text-muted-foreground/60">
                ⌘↵ / Ctrl↵
              </span>
            </div>
          </div>
        )}

        <Button
          onClick={handleLookup}
          disabled={looking || !canSubmit}
          variant="outline"
          size="sm"
          className="w-full border-primary/40 bg-primary/10 text-primary hover:bg-primary/20"
        >
          {looking ? (
            <IconLoader2 size={15} stroke={2} data-icon="inline-start" className="animate-spin" />
          ) : (
            <IconSearch size={15} stroke={2} data-icon="inline-start" />
          )}
          {looking ? "Looking up…" : "Look up"}
        </Button>

        <AnimatePresence>
          {error && (
            <motion.div
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
            >
              <Alert variant="destructive">
                <IconAlertTriangle size={13} stroke={2} />
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            </motion.div>
          )}
        </AnimatePresence>
      </div>

      {/* ── Results ── */}
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
                aria-hidden="true"
              />
              <span className="text-xs font-medium text-foreground">
                Results
              </span>
              <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                {results.length}
              </span>
            </div>

            <Table aria-label="Transaction lookup results">
              <TableHeader>
                <TableRow className="bg-muted/30">
                  {["TXID", "Status", "Confirmations", "Block height"].map(
                    (h) => (
                      <TableHead
                        key={h}
                        scope="col"
                        className="whitespace-nowrap text-[11px]"
                      >
                        {h}
                      </TableHead>
                    ),
                  )}
                </TableRow>
              </TableHeader>
              <TableBody>
                {results.map(([txid, result]) => (
                  <TxResultRow key={txid} txid={txid} result={result} />
                ))}
              </TableBody>
            </Table>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
