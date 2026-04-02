"use client";

import { useRef, useState } from "react";
import {
  IconChevronRight,
  IconLoader2,
  IconX,
} from "@tabler/icons-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { cn } from "@/lib/utils";
import {
  formatFeedEventTime,
  getPayloadNumber,
  getPayloadOutputs,
  getPayloadString,
  getPayloadStrings,
} from "@/features/bitcoin/events/lib/feed-formatters";
import { getHeading } from "@/features/bitcoin/events/lib/event-helpers";
import type { BtcEvent } from "@/features/bitcoin/types";
import type { BlockDetailsResult } from "@/lib/api/bitcoin";
import { EVENT_CFG } from "../lib/event-cfg";
import { FieldRow } from "./field-row";

// ── SectionHeading ─────────────────────────────────────────────────────────────

function SectionHeading({ children }: { children: React.ReactNode }) {
  return (
    <p className="mb-1.5 mt-1 pt-2.5 text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground/60 border-t border-border/50">
      {children}
    </p>
  );
}

// ── EventDetail ────────────────────────────────────────────────────────────────

export interface EventDetailProps {
  event: BtcEvent;
  blockDetails: BlockDetailsResult | null;
  blockError: string | null;
  loadingBlock: boolean;
  onClose: () => void;
}

export function EventDetail({
  event,
  blockDetails,
  blockError,
  loadingBlock,
  onClose,
}: EventDetailProps) {
  const cfg = EVENT_CFG[event.type];
  const [copied, setCopied] = useState<string | null>(null);
  const copiedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  function handleCopy(value: string, key: string) {
    navigator.clipboard.writeText(value).catch(() => {});
    setCopied(key);
    if (copiedTimerRef.current !== null) clearTimeout(copiedTimerRef.current);
    copiedTimerRef.current = setTimeout(() => {
      setCopied(null);
      copiedTimerRef.current = null;
    }, 1_500);
  }

  // ── Payload extraction ───────────────────────────────────────────────────
  const payload = event.payload;
  const txid = getPayloadString(payload, "txid");
  const replacedTxid = getPayloadString(payload, "replaced_txid");
  const replacementTxid = getPayloadString(payload, "replacement_txid");
  const blockHash = getPayloadString(payload, "hash", "block");
  const blockHeight = getPayloadNumber(payload, "height");
  const confirmations = getPayloadNumber(payload, "confirmations");
  const feeRate = getPayloadNumber(payload, "fee_rate");
  const txCount = getPayloadNumber(payload, "tx_count");
  const blockSize = getPayloadString(payload, "size");
  const status = getPayloadString(payload, "status");
  const addresses = getPayloadStrings(payload, "addresses");
  const outputs = getPayloadOutputs(payload);

  // ── Field list ───────────────────────────────────────────────────────────
  type Field = {
    label: string;
    value: string;
    copyKey?: string;
    accentClass?: string;
  };

  const fields: Field[] = (
    [
      txid ? { label: "TXID", value: txid, copyKey: "txid" } : null,
      replacedTxid
        ? {
            label: "Replaced",
            value: replacedTxid,
            copyKey: "replaced",
            accentClass: "text-destructive",
          }
        : null,
      replacementTxid
        ? { label: "New TXID", value: replacementTxid, copyKey: "replacement" }
        : null,
      blockHeight !== null
        ? {
            label: "Height",
            value: `#${blockHeight.toLocaleString()}`,
            accentClass: "text-blue-600 dark:text-blue-400 font-semibold",
          }
        : null,
      txCount !== null
        ? { label: "Txns", value: txCount.toLocaleString() }
        : null,
      blockSize ? { label: "Size", value: blockSize } : null,
      feeRate !== null
        ? {
            label: "Rate",
            value: `${feeRate} sat/vB`,
            accentClass: "text-amber-600 dark:text-amber-300 font-semibold",
          }
        : null,
      confirmations !== null
        ? {
            label: "Confirms",
            value: confirmations.toLocaleString(),
            accentClass: "text-green-600 dark:text-green-400",
          }
        : null,
      status ? { label: "Status", value: status } : null,
      blockHash
        ? {
            label: event.type === "new_block" ? "Hash" : "Block",
            value: blockHash,
            copyKey: "hash",
          }
        : null,
      { label: "At", value: new Date(event.receivedAt).toLocaleString() },
    ] satisfies Array<Field | null>
  ).filter((f): f is Field => f !== null);

  // ── Lifecycle chips ──────────────────────────────────────────────────────
  const lifecycle: string[] | null =
    event.type === "pending_mempool" || event.type === "confirmed_tx"
      ? ["Mempool", "Confirmed"]
      : event.type === "mempool_replaced"
        ? ["Mempool", "Replaced"]
        : null;

  // ── Block snapshot rows ──────────────────────────────────────────────────
  const blockSnapshotFields: Array<Field & { copyKey?: string }> = blockDetails
    ? [
        {
          label: "Mined",
          value: new Date(blockDetails.time * 1_000).toLocaleString(),
        },
        { label: "Txns", value: blockDetails.tx_count.toLocaleString() },
        {
          label: "Confirms",
          value: blockDetails.confirmations.toLocaleString(),
        },
        { label: "Bits", value: blockDetails.bits },
        { label: "Nonce", value: blockDetails.nonce.toLocaleString() },
        {
          label: "Merkle",
          value: blockDetails.merkle_root,
          copyKey: "merkle",
        },
        {
          label: "Work",
          value: blockDetails.chainwork,
          copyKey: "chainwork",
        },
      ]
    : [];

  return (
    <aside
      aria-label="Event inspector"
      className="flex h-full w-full flex-col bg-background border-r border-border/60"
    >
      {/* ── Header ── */}
      <div className="flex items-center gap-2.5 border-b border-border/60 px-4 py-3.5 shrink-0">
        {/* Tinted icon box — uses same badge tokens as the chip */}
        <div
          className={cn(
            "flex size-8 shrink-0 items-center justify-center rounded-lg border",
            cfg.badge,
          )}
          aria-hidden="true"
        >
          <span className={cn("size-2 rounded-full", cfg.dot)} />
        </div>

        <div className="min-w-0 flex-1">
          <p className="truncate text-[13px] font-semibold text-foreground leading-tight">
            {getHeading(event)}
          </p>
          <p className="font-mono text-[10px] text-muted-foreground">
            {formatFeedEventTime(event.receivedAt)}
          </p>
        </div>

        <button
          type="button"
          onClick={onClose}
          aria-label="Close inspector"
          className="shrink-0 rounded-md p-1 text-muted-foreground/50 transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <IconX size={13} stroke={1.8} />
        </button>
      </div>

      {/* ── Scrollable body ── */}
      <div className="flex-1 overflow-y-auto px-4 pb-2">
        {/* Primary fields */}
        {fields.map((field) => (
          <FieldRow
            key={field.label}
            label={field.label}
            value={field.value}
            copyKey={field.copyKey}
            copied={copied}
            onCopy={handleCopy}
            accentClass={field.accentClass}
          />
        ))}

        {/* Addresses */}
        {addresses.length > 0 && (
          <>
            <SectionHeading>Addresses</SectionHeading>
            {addresses.map((addr) => (
              <FieldRow
                key={addr}
                label="Addr"
                value={addr}
                copyKey={`addr-${addr}`}
                copied={copied}
                onCopy={handleCopy}
              />
            ))}
          </>
        )}

        {/* Outputs */}
        {outputs.length > 0 && (
          <>
            <SectionHeading>Outputs</SectionHeading>
            {outputs.map((out) => (
              <FieldRow
                key={`${out.address}-${out.amountSat}`}
                label="Out"
                value={
                  out.amountSat === null
                    ? out.address
                    : `${out.address} · ${out.amountSat.toLocaleString()} sat`
                }
                copyKey={`out-${out.address}`}
                copied={copied}
                onCopy={handleCopy}
              />
            ))}
          </>
        )}

        {/* Block snapshot (new_block only) */}
        {event.type === "new_block" && (
          <>
            <div className="mt-1 flex items-center justify-between gap-2 pt-2.5">
              <p className="text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground/60">
                Block snapshot
              </p>
              {loadingBlock && (
                <span className="inline-flex items-center gap-1 rounded-full border border-blue-500/30 bg-blue-500/10 px-2 py-0.5 text-[10px] font-medium text-blue-700 dark:text-blue-300">
                  <IconLoader2 size={9} stroke={2} className="animate-spin" />
                  Loading
                </span>
              )}
            </div>
            {blockError && (
              <Alert variant="destructive" className="mt-2">
                <AlertDescription className="text-[11px]">
                  {blockError}
                </AlertDescription>
              </Alert>
            )}
            {blockSnapshotFields.map((row) => (
              <FieldRow
                key={row.label}
                label={row.label}
                value={row.value}
                copyKey={row.copyKey}
                copied={copied}
                onCopy={handleCopy}
              />
            ))}
          </>
        )}
      </div>

      {/* ── Lifecycle footer ── */}
      {lifecycle && (
        <div className="shrink-0 border-t border-border/60 px-4 py-3">
          <p className="mb-2 text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground/60">
            Lifecycle
          </p>
          <div className="flex items-center gap-1">
            {lifecycle.map((step, i) => {
              const active =
                (event.type === "pending_mempool" && step === "Mempool") ||
                (event.type === "confirmed_tx" && step === "Confirmed") ||
                (event.type === "mempool_replaced" && step === "Replaced");
              const complete =
                step === "Mempool" &&
                (event.type === "confirmed_tx" ||
                  event.type === "mempool_replaced");
              return (
                <div key={step} className="flex items-center gap-1">
                  <span
                    className={cn(
                      "rounded-full border px-2.5 py-1 text-[10px] font-medium",
                      active &&
                        "border-foreground/15 bg-foreground/10 text-foreground",
                      complete &&
                        "border-green-500/20 bg-green-500/10 text-green-700 dark:text-green-400",
                      !active &&
                        !complete &&
                        "border-border/50 bg-muted/50 text-muted-foreground",
                    )}
                  >
                    {step}
                  </span>
                  {i < lifecycle.length - 1 && (
                    <IconChevronRight
                      size={10}
                      stroke={2}
                      className="text-muted-foreground/40"
                      aria-hidden="true"
                    />
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}
    </aside>
  );
}
