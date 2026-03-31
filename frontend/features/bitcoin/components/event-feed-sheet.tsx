"use client";

import { useEffect, useRef, useState, useTransition } from "react";
import {
  IconActivity,
  IconAlertTriangle,
  IconChevronRight,
  IconCopy,
  IconLoader2,
  IconRefresh,
  IconRefreshAlert,
  IconX,
} from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { getBlock } from "@/lib/api/bitcoin";
import type { BlockDetailsResult } from "@/lib/api/bitcoin";

type FeedEventType =
  | "ping"
  | "new_block"
  | "pending_mempool"
  | "confirmed_tx"
  | "mempool_replaced"
  | "stream_requires_reregistration";

type FeedConnState = "connecting" | "connected" | "reconnecting" | "error";

type FeedEvent = {
  id: string;
  type: FeedEventType;
  payload: Record<string, unknown>;
  receivedAt: number;
};

type ReceivedOutput = {
  address: string;
  amountSat: number | null;
};

const EVENT_CFG: Record<
  FeedEventType,
  { label: string; dot: string; badge: string }
> = {
  ping: {
    label: "Ping",
    dot: "bg-muted-foreground/40",
    badge: "border-border/60 bg-muted/40 text-muted-foreground",
  },
  new_block: {
    label: "New block",
    dot: "bg-blue-500",
    badge: "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300",
  },
  pending_mempool: {
    label: "Mempool",
    dot: "bg-amber-500",
    badge: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
  confirmed_tx: {
    label: "Confirmed",
    dot: "bg-green-500",
    badge: "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-300",
  },
  mempool_replaced: {
    label: "Replaced",
    dot: "bg-destructive",
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
  },
  stream_requires_reregistration: {
    label: "Re-register",
    dot: "bg-amber-500 animate-pulse",
    badge: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
};

const STATE_CFG: Record<
  FeedConnState,
  { label: string; dot: string; badge: string }
> = {
  connecting: {
    label: "Connecting",
    dot: "bg-amber-500 animate-pulse",
    badge: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
  connected: {
    label: "Live",
    dot: "bg-green-500",
    badge: "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-300",
  },
  reconnecting: {
    label: "Reconnecting",
    dot: "bg-amber-500 animate-pulse",
    badge: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
  error: {
    label: "Error",
    dot: "bg-destructive",
    badge: "border-destructive/30 bg-destructive/10 text-destructive",
  },
};

const BLOCK_DETAILS_TTL_MS = 5 * 60 * 1000;
const BLOCK_ERROR_TTL_MS = 30 * 1000;

const blockDetailsCache = new Map<
  string,
  { data: BlockDetailsResult; expiresAt: number }
>();
const blockDetailsInflight = new Map<string, Promise<BlockDetailsResult>>();
const blockDetailsErrorCache = new Map<
  string,
  { message: string; expiresAt: number }
>();

function readString(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function readNumber(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim()) {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return null;
}

function readStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((entry) => readString(entry))
    .filter((entry): entry is string => entry !== null);
}

function getPayloadString(payload: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const value = readString(payload[key]);
    if (value) return value;
  }
  return null;
}

function getPayloadNumber(payload: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const value = readNumber(payload[key]);
    if (value !== null) return value;
  }
  return null;
}

function getPayloadStrings(payload: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    const values = readStringArray(payload[key]);
    if (values.length > 0) return values;
  }
  return [];
}

function getPayloadOutputs(payload: Record<string, unknown>): ReceivedOutput[] {
  const source = Array.isArray(payload.received_outputs) ? payload.received_outputs : [];
  return source
    .map((entry) => {
      if (!entry || typeof entry !== "object") return null;
      const output = entry as Record<string, unknown>;
      const address = readString(output.address) ?? readString(output.Address);
      if (!address) return null;
      return {
        address,
        amountSat: readNumber(output.amount_sat) ?? readNumber(output.AmountSat),
      };
    })
    .filter((entry): entry is ReceivedOutput => entry !== null);
}

export function formatFeedEventTime(value: number) {
  return new Date(value).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function getFeedEventLabel(type: FeedEventType) {
  return EVENT_CFG[type].label;
}

export function getFeedEventSummary(event: FeedEvent) {
  if (event.type === "new_block") {
    const height = getPayloadNumber(event.payload, "height");
    return `Block #${height?.toLocaleString() ?? "?"}`;
  }
  if (event.type === "pending_mempool") {
    const txid = getPayloadString(event.payload, "txid");
    const feeRate = getPayloadNumber(event.payload, "fee_rate");
    return `${txid?.slice(0, 14) ?? "—"}… · ${feeRate ?? 0} sat/vB`;
  }
  if (event.type === "confirmed_tx") {
    const txid = getPayloadString(event.payload, "txid");
    const height = getPayloadNumber(event.payload, "height");
    return `${txid?.slice(0, 14) ?? "—"}… @ #${height?.toLocaleString() ?? "?"}`;
  }
  if (event.type === "mempool_replaced") {
    const txid = getPayloadString(event.payload, "replaced_txid");
    return `Replaced ${txid?.slice(0, 14) ?? "—"}…`;
  }
  if (event.type === "stream_requires_reregistration") {
    return "Watch addresses expired. Re-register to resume updates.";
  }
  return "Keep-alive heartbeat";
}

function getBlockHashFromEvent(event: FeedEvent) {
  const hash = event.type === "new_block" ? getPayloadString(event.payload, "hash") : null;
  return hash && /^[0-9a-fA-F]{64}$/.test(hash) ? hash.toLowerCase() : null;
}

function EventTypeBadge({ event }: { event: FeedEvent }) {
  const cfg = EVENT_CFG[event.type];
  return (
    <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium", cfg.badge)}>
      <span className={cn("size-1.5 rounded-full", cfg.dot)} />
      {cfg.label}
    </span>
  );
}

function CopyButton({
  value,
  copied,
  onCopy,
}: {
  value: string;
  copied: boolean;
  onCopy: (value: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onCopy(value)}
      className="inline-flex items-center gap-1 rounded-md px-1.5 py-1 text-[10px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <IconCopy size={11} stroke={1.8} />
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

function StatTile({
  label,
  value,
}: {
  label: string;
  value: string;
}) {
  return (
    <div className="rounded-xl border border-border/70 bg-background/70 px-3 py-2.5">
      <p className="text-[10px] uppercase tracking-[0.14em] text-muted-foreground/70">{label}</p>
      <p className="mt-1 font-mono text-sm font-semibold text-foreground">{value}</p>
    </div>
  );
}

function DetailRow({
  label,
  value,
  copyValue,
  copied,
  onCopy,
  accentClass,
}: {
  label: string;
  value: string;
  copyValue?: string;
  copied: string | null;
  onCopy: (value: string) => void;
  accentClass?: string;
}) {
  return (
    <div className="flex flex-col gap-2 border-b border-border/60 py-3 last:border-b-0">
      <p className="text-[10px] uppercase tracking-[0.14em] text-muted-foreground/70">{label}</p>
      <div className="flex items-center justify-between gap-3">
        <p className={cn("min-w-0 break-all font-mono text-[12px] font-medium text-foreground", accentClass)}>
          {value}
        </p>
        {copyValue ? (
          <CopyButton value={copyValue} copied={copied === copyValue} onCopy={onCopy} />
        ) : (
          <span />
        )}
      </div>
    </div>
  );
}

function getInspectorHeading(event: FeedEvent) {
  if (event.type === "new_block") {
    return getFeedEventSummary(event);
  }
  if (event.type === "pending_mempool") {
    return "Mempool event";
  }
  if (event.type === "confirmed_tx") {
    return "Confirmed transaction";
  }
  if (event.type === "mempool_replaced") {
    return "Replacement event";
  }
  if (event.type === "stream_requires_reregistration") {
    return "Registration update";
  }
  return "Live event";
}

function getFeedHeightStat(events: FeedEvent[]) {
  for (const event of events) {
    const height = getPayloadNumber(event.payload, "height");
    if (height !== null) {
      return `#${height.toLocaleString()}`;
    }
  }
  return "—";
}

function getMempoolStat(events: FeedEvent[]) {
  const count = events.filter((event) => event.type === "pending_mempool").length;
  return count > 0 ? count.toLocaleString() : "—";
}

function getLiveStat(
  connState: FeedConnState,
  lastHeartbeatAt: number | null,
  retryCount: number,
) {
  if (lastHeartbeatAt !== null) {
    return formatFeedEventTime(lastHeartbeatAt);
  }
  if (connState === "reconnecting" && retryCount > 0) {
    return `Retry · ${retryCount}`;
  }
  return STATE_CFG[connState].label;
}

function getStatusMeta(
  connState: FeedConnState,
  retryCount: number,
) {
  if (connState === "reconnecting" && retryCount > 0) {
    return `try ${retryCount}`;
  }
  return null;
}

async function getBlockDetailsCached(hash: string) {
  const normalizedHash = hash.toLowerCase();
  const now = Date.now();
  const cached = blockDetailsCache.get(normalizedHash);
  if (cached && cached.expiresAt > now) {
    return cached.data;
  }

  const cachedError = blockDetailsErrorCache.get(normalizedHash);
  if (cachedError && cachedError.expiresAt > now) {
    throw new Error(cachedError.message);
  }

  const inflight = blockDetailsInflight.get(normalizedHash);
  if (inflight) {
    return inflight;
  }

  const request = getBlock(normalizedHash)
    .then((data) => {
      blockDetailsCache.set(normalizedHash, {
        data,
        expiresAt: Date.now() + BLOCK_DETAILS_TTL_MS,
      });
      blockDetailsErrorCache.delete(normalizedHash);
      return data;
    })
    .catch((error: unknown) => {
      const message =
        error instanceof Error ? error.message : "Failed to load block details. Try again.";
      blockDetailsErrorCache.set(normalizedHash, {
        message,
        expiresAt: Date.now() + BLOCK_ERROR_TTL_MS,
      });
      throw new Error(message);
    })
    .finally(() => {
      blockDetailsInflight.delete(normalizedHash);
    });

  blockDetailsInflight.set(normalizedHash, request);
  return request;
}

function EventRow({
  event,
  selected,
  loading,
  onSelect,
}: {
  event: FeedEvent;
  selected: boolean;
  loading: boolean;
  onSelect: (event: FeedEvent) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onSelect(event)}
      className={cn(
        "relative grid w-full grid-cols-[auto_auto_minmax(0,1fr)_auto] items-center gap-3 border-b border-border/60 px-4 py-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        selected ? "bg-muted/70" : "hover:bg-muted/35",
      )}
    >
      {selected && <span className="absolute inset-y-0 left-0 w-0.5 rounded-r-full bg-foreground" />}
      <span className={cn("size-1.5 rounded-full", EVENT_CFG[event.type].dot)} />
      <EventTypeBadge event={event} />
      <div className="min-w-0">
        <p className="truncate font-mono text-[12px] text-foreground">{getFeedEventSummary(event)}</p>
      </div>
      <span className="font-mono text-[10px] text-muted-foreground/70">
        {loading ? "Loading…" : formatFeedEventTime(event.receivedAt)}
      </span>
    </button>
  );
}

function EventInspector({
  event,
  blockDetails,
  blockError,
  loadingBlock,
  onClose,
}: {
  event: FeedEvent;
  blockDetails: BlockDetailsResult | null;
  blockError: string | null;
  loadingBlock: boolean;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState<string | null>(null);
  const copiedTimerRef = useRef<number | null>(null);
  const payload = event.payload;
  const txid = getPayloadString(payload, "txid");
  const replacedTxid = getPayloadString(payload, "replaced_txid");
  const replacementTxid = getPayloadString(payload, "replacement_txid");
  const blockHash = getPayloadString(payload, "hash", "block");
  const blockHeight = getPayloadNumber(payload, "height");
  const confirmations = getPayloadNumber(payload, "confirmations");
  const feeRate = getPayloadNumber(payload, "fee_rate");
  const status = getPayloadString(payload, "status");
  const addresses = getPayloadStrings(payload, "addresses");
  const outputs = getPayloadOutputs(payload);
  const lifecycle =
    event.type === "pending_mempool"
      ? ["Mempool", "Confirmed"]
      : event.type === "confirmed_tx"
        ? ["Mempool", "Confirmed"]
        : event.type === "mempool_replaced"
          ? ["Mempool", "Replaced"]
          : null;

  useEffect(() => {
    return () => {
      if (copiedTimerRef.current !== null) {
        window.clearTimeout(copiedTimerRef.current);
      }
    };
  }, []);

  function handleCopy(value: string) {
    navigator.clipboard.writeText(value).catch(() => undefined);
    setCopied(value);
    if (copiedTimerRef.current !== null) {
      window.clearTimeout(copiedTimerRef.current);
    }
    copiedTimerRef.current = window.setTimeout(() => {
      setCopied(null);
      copiedTimerRef.current = null;
    }, 1500);
  }

  type Field = {
    label: string;
    value: string;
    copyValue?: string;
    accentClass?: string;
  };

  const fields = [
    txid ? { label: "TXID", value: txid, copyValue: txid } : null,
    replacedTxid ? { label: "Replaced", value: replacedTxid, copyValue: replacedTxid } : null,
    replacementTxid ? { label: "Replacement", value: replacementTxid, copyValue: replacementTxid } : null,
    blockHeight !== null ? { label: "Height", value: `#${blockHeight.toLocaleString()}` } : null,
    feeRate !== null ? { label: "Rate", value: `${feeRate} sat/vB`, accentClass: "text-amber-600 dark:text-amber-300" } : null,
    status ? { label: "Status", value: status } : null,
    blockHash ? { label: event.type === "new_block" ? "Hash" : "Block", value: blockHash, copyValue: blockHash } : null,
    { label: "At", value: new Date(event.receivedAt).toLocaleString() },
  ] as Array<Field | null>;

  const nonNullFields: Field[] = fields.filter((field): field is Field => field !== null);

  const blockSnapshotRows = blockDetails
    ? [
        { label: "Mined", value: new Date(blockDetails.time * 1000).toLocaleString() },
        { label: "Median", value: new Date(blockDetails.median_time * 1000).toLocaleString() },
        { label: "Confirm", value: blockDetails.confirmations.toLocaleString() },
        { label: "TXs", value: blockDetails.tx_count.toLocaleString() },
        { label: "Bits", value: blockDetails.bits },
        { label: "Nonce", value: blockDetails.nonce.toLocaleString() },
        { label: "Merkle", value: blockDetails.merkle_root, copyValue: blockDetails.merkle_root },
        { label: "Chainwork", value: blockDetails.chainwork, copyValue: blockDetails.chainwork },
      ]
    : [];

  return (
    <aside className="flex h-full w-full flex-col border-b border-border/60 bg-card/90 md:w-[30rem] md:shrink-0 md:border-r md:border-b-0">
      <div className="flex items-start justify-between gap-3 border-b border-border/60 px-4 py-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 mb-2">
            <p className="text-sm font-semibold text-foreground">{getInspectorHeading(event)}</p>
            <EventTypeBadge event={event} />
          </div>
          <p className="font-mono text-[11px] text-muted-foreground">
            {formatFeedEventTime(event.receivedAt)}
          </p>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <IconX size={14} stroke={1.8} />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto px-4 py-2">
        <div>
          {nonNullFields.map((field) => (
            <DetailRow
              key={`${field.label}-${field.value}`}
              label={field.label}
              value={field.value}
              copyValue={field.copyValue}
              copied={copied}
              onCopy={handleCopy}
              accentClass={field.accentClass}
            />
          ))}
        </div>

        {lifecycle && (
          <div className="border-t border-border/60 py-4">
            <p className="text-[10px] uppercase tracking-[0.14em] text-muted-foreground/70">Lifecycle</p>
            <div className="mt-3 flex items-center gap-1.5">
              {lifecycle.map((step, index) => {
                const active =
                  (event.type === "pending_mempool" && step === "Mempool") ||
                  (event.type === "confirmed_tx" && step === "Confirmed") ||
                  (event.type === "mempool_replaced" && step === "Replaced");
                const complete =
                  step === "Mempool" &&
                  (event.type === "confirmed_tx" || event.type === "mempool_replaced");

                return (
                  <div key={step} className="flex items-center gap-1.5">
                    <span
                      className={cn(
                        "rounded-full border px-2.5 py-1 text-[10px] font-medium",
                        active && "border-foreground/15 bg-foreground/8 text-foreground",
                        complete && "border-green-500/20 bg-green-500/10 text-green-700 dark:text-green-300",
                        !active && !complete && "border-border/60 bg-muted/50 text-muted-foreground",
                      )}
                    >
                      {step}
                    </span>
                    {index < lifecycle.length - 1 && (
                      <IconChevronRight size={11} stroke={1.8} className="text-muted-foreground/60" />
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {addresses.length > 0 && (
          <div className="border-t border-border/60 py-4">
            <p className="text-[10px] uppercase tracking-[0.14em] text-muted-foreground/70">Addresses</p>
            <div className="mt-2">
              {addresses.map((address) => (
                <DetailRow
                  key={address}
                  label="Address"
                  value={address}
                  copyValue={address}
                  copied={copied}
                  onCopy={handleCopy}
                />
              ))}
            </div>
          </div>
        )}

        {outputs.length > 0 && (
          <div className="border-t border-border/60 py-4">
            <p className="text-[10px] uppercase tracking-[0.14em] text-muted-foreground/70">Received outputs</p>
            <div className="mt-2">
              {outputs.map((output) => (
                <DetailRow
                  key={`${output.address}-${output.amountSat ?? "na"}`}
                  label="Output"
                  value={
                    output.amountSat === null
                      ? output.address
                      : `${output.address} · ${output.amountSat.toLocaleString()} sat`
                  }
                  copied={copied}
                  onCopy={handleCopy}
                  copyValue={output.address}
                />
              ))}
            </div>
          </div>
        )}

        {event.type === "new_block" && (
          <div className="border-t border-border/60 py-4">
            <div className="flex items-center justify-between gap-3">
              <p className="text-[10px] uppercase tracking-[0.14em] text-muted-foreground/70">Block snapshot</p>
              {loadingBlock && (
                <span className="inline-flex items-center gap-1 rounded-full border border-blue-500/30 bg-blue-500/10 px-2 py-0.5 text-[10px] font-medium text-blue-700 dark:text-blue-300">
                  <IconLoader2 size={10} stroke={2} className="animate-spin" />
                  Loading
                </span>
              )}
            </div>

            {blockError && (
              <div className="mt-3 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-[11px] text-destructive">
                {blockError}
              </div>
            )}

            {blockSnapshotRows.length > 0 && (
              <div className="mt-2">
                {blockSnapshotRows.map((row) => (
                  <DetailRow
                    key={`${row.label}-${row.value}`}
                    label={row.label}
                    value={row.value}
                    copyValue={row.copyValue}
                    copied={copied}
                    onCopy={handleCopy}
                  />
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </aside>
  );
}

export function EventFeedSheet({
  open,
  onOpenChange,
  connState,
  events,
  error,
  retryCount,
  lastHeartbeatAt,
  needsReregistration,
  onClearEvents,
  onRetryNow,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  connState: FeedConnState;
  events: FeedEvent[];
  error: string | null;
  retryCount: number;
  lastHeartbeatAt: number | null;
  needsReregistration: boolean;
  onClearEvents: () => void;
  onRetryNow: () => void;
}) {
  const [selectedEventId, setSelectedEventId] = useState<string | null>(null);
  const [blockDetails, setBlockDetails] = useState<BlockDetailsResult | null>(null);
  const [blockError, setBlockError] = useState<string | null>(null);
  const [loadingBlockHash, setLoadingBlockHash] = useState<string | null>(null);
  const [loadingBlock, startBlockLookup] = useTransition();
  const latestBlockRequestRef = useRef<string | null>(null);
  const stateCfg = STATE_CFG[connState];
  const selectedEvent = events.find((event) => event.id === selectedEventId) ?? null;
  const heightStat = getFeedHeightStat(events);
  const mempoolStat = getMempoolStat(events);
  const liveStat = getLiveStat(connState, lastHeartbeatAt, retryCount);
  const statusMeta = getStatusMeta(connState, retryCount);

  function clearSelection() {
    latestBlockRequestRef.current = null;
    setSelectedEventId(null);
    setBlockDetails(null);
    setBlockError(null);
    setLoadingBlockHash(null);
  }

  function handleSelectEvent(event: FeedEvent) {
    if (selectedEventId === event.id) {
      clearSelection();
      return;
    }

    setSelectedEventId(event.id);
    setBlockError(null);
    const blockHash = getBlockHashFromEvent(event);
    if (!blockHash) {
      latestBlockRequestRef.current = null;
      setBlockDetails(null);
      setLoadingBlockHash(null);
      return;
    }

    latestBlockRequestRef.current = blockHash;
    setLoadingBlockHash(blockHash);
    startBlockLookup(async () => {
      try {
        const details = await getBlockDetailsCached(blockHash);
        if (latestBlockRequestRef.current !== blockHash) return;
        setBlockDetails(details);
      } catch (err: unknown) {
        if (latestBlockRequestRef.current !== blockHash) return;
        setBlockDetails(null);
        setBlockError(
          err instanceof Error ? err.message : "Failed to load block details. Try again.",
        );
      } finally {
        if (latestBlockRequestRef.current !== blockHash) return;
        setLoadingBlockHash(null);
      }
    });
  }

  function handleOpenChange(nextOpen: boolean) {
    onOpenChange(nextOpen);
    if (!nextOpen) clearSelection();
  }

  useEffect(() => {
    if (selectedEventId && !events.some((event) => event.id === selectedEventId)) {
      clearSelection();
    }
  }, [events, selectedEventId]);

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent
        side="right"
        showCloseButton={false}
        className="overflow-visible p-0 data-[side=right]:w-[min(100vw,26.25rem)] data-[side=right]:sm:max-w-[26.25rem]"
      >
        <div className="relative h-full min-h-0">
          {selectedEvent && (
            <div className="absolute inset-y-0 right-full hidden md:block">
              <EventInspector
                event={selectedEvent}
                blockDetails={blockDetails}
                blockError={blockError}
                loadingBlock={loadingBlock || loadingBlockHash === getBlockHashFromEvent(selectedEvent)}
                onClose={clearSelection}
              />
            </div>
          )}

          <section className="flex h-full min-h-0 w-full min-w-0 flex-col bg-background">
            <div className="border-b border-border/60 px-4 py-4">
              <div className="flex items-start justify-between gap-3">
                <div className="flex min-w-0 items-center gap-2">
                  <SheetTitle className="text-sm font-semibold">Event Feed</SheetTitle>
                  <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono text-[10px] text-foreground">
                    {events.length}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => handleOpenChange(false)}
                    className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <IconX size={14} stroke={1.8} />
                  </button>
                </div>
              </div>

              <SheetDescription className="sr-only">
                Live Bitcoin event feed with a side-by-side event inspector.
              </SheetDescription>

              <div className="mt-4 flex items-center justify-between gap-3">
                <div className="flex min-w-0 items-center gap-2">
                  <span className={cn("size-1.5 rounded-full", stateCfg.dot)} />
                  <p className="truncate text-sm font-medium text-foreground">Bitcoin mainnet</p>
                </div>
                <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium", stateCfg.badge)}>
                  <span className={cn("size-1.5 rounded-full", stateCfg.dot)} />
                  {stateCfg.label}
                  {statusMeta ? ` · ${statusMeta}` : ""}
                </span>
              </div>

              <div className="mt-4 grid grid-cols-3 gap-2">
                <StatTile label="Height" value={heightStat} />
                <StatTile label="Mempool" value={mempoolStat} />
                <StatTile label="Live" value={liveStat} />
              </div>
            </div>

            {(error || needsReregistration) && (
              <div className="border-b border-border/60 px-4 py-3">
                <div className="flex flex-col gap-2">
                  {error && (
                    <div className="flex items-start justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                      <div className="flex items-start gap-2">
                        <IconAlertTriangle size={13} stroke={2} className="mt-0.5 shrink-0" />
                        <p>{error}</p>
                      </div>
                      {(connState === "error" || connState === "reconnecting") && (
                        <Button
                          variant="outline"
                          size="xs"
                          onClick={onRetryNow}
                          className="border-destructive/30 bg-background/60 text-destructive hover:bg-background"
                        >
                          <IconRefresh size={12} stroke={2} />
                          Retry
                        </Button>
                      )}
                    </div>
                  )}

                  {needsReregistration && (
                    <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
                      <IconRefreshAlert size={13} stroke={2} className="mt-0.5 shrink-0" />
                      <p>Watch addresses expired. Re-register them in the Addresses tab to resume live updates.</p>
                    </div>
                  )}
                </div>
              </div>
            )}

            <div className="flex items-center justify-between border-b border-border/60 px-4 py-3">
              <p className="text-[11px] font-medium uppercase tracking-[0.14em] text-muted-foreground/70">Events</p>
              {events.length > 0 && (
                <button
                  type="button"
                  onClick={() => {
                    clearSelection();
                    onClearEvents();
                  }}
                  className="text-[11px] text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none"
                >
                  Clear
                </button>
              )}
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto">
              {events.length === 0 ? (
                <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
                  {connState === "error" ? (
                    <>
                      <IconAlertTriangle size={22} stroke={1.25} className="text-muted-foreground/30" />
                      <p className="text-xs">The stream is unavailable right now.</p>
                    </>
                  ) : (
                    <>
                      <IconActivity size={22} stroke={1.25} className="text-muted-foreground/40" />
                      <p className="text-xs">Waiting for blockchain events…</p>
                    </>
                  )}
                </div>
              ) : (
                events.map((event) => (
                  <EventRow
                    key={event.id}
                    event={event}
                    selected={event.id === selectedEventId}
                    loading={loadingBlockHash !== null && loadingBlockHash === getBlockHashFromEvent(event)}
                    onSelect={handleSelectEvent}
                  />
                ))
              )}
            </div>
          </section>
        </div>
      </SheetContent>
    </Sheet>
  );
}
