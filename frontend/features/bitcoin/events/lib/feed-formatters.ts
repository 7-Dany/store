/**
 * Pure formatting and payload-extraction helpers for the Bitcoin event feed.
 * No React imports — fully unit-testable without a DOM.
 */

import type { BtcEvent, BtcEventType } from "@/features/bitcoin/types";

// ── Scalar readers ─────────────────────────────────────────────────────────────

export function readString(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

export function readNumber(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim()) {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return null;
}

export function readStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((entry) => readString(entry))
    .filter((entry): entry is string => entry !== null);
}

// ── Payload accessors ──────────────────────────────────────────────────────────

export function getPayloadString(
  payload: Record<string, unknown>,
  ...keys: string[]
): string | null {
  for (const key of keys) {
    const value = readString(payload[key]);
    if (value) return value;
  }
  return null;
}

export function getPayloadNumber(
  payload: Record<string, unknown>,
  ...keys: string[]
): number | null {
  for (const key of keys) {
    const value = readNumber(payload[key]);
    if (value !== null) return value;
  }
  return null;
}

export function getPayloadStrings(
  payload: Record<string, unknown>,
  ...keys: string[]
): string[] {
  for (const key of keys) {
    const values = readStringArray(payload[key]);
    if (values.length > 0) return values;
  }
  return [];
}

export type ReceivedOutput = { address: string; amountSat: number | null };

export function getPayloadOutputs(
  payload: Record<string, unknown>,
): ReceivedOutput[] {
  const source = Array.isArray(payload.received_outputs)
    ? payload.received_outputs
    : [];
  return source
    .map((entry) => {
      if (!entry || typeof entry !== "object") return null;
      const o = entry as Record<string, unknown>;
      const address = readString(o.address) ?? readString(o.Address);
      if (!address) return null;
      return {
        address,
        amountSat: readNumber(o.amount_sat) ?? readNumber(o.AmountSat),
      };
    })
    .filter((entry): entry is ReceivedOutput => entry !== null);
}

// ── Display helpers ────────────────────────────────────────────────────────────

export function formatFeedEventTime(value: number): string {
  return new Date(value).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function getFeedEventLabel(type: BtcEventType): string {
  const labels: Record<BtcEventType, string> = {
    ping: "Ping",
    new_block: "New block",
    pending_mempool: "Mempool",
    confirmed_tx: "Confirmed",
    mempool_replaced: "Replaced",
    stream_requires_reregistration: "Re-register",
  };
  return labels[type];
}

export function getFeedEventSummary(event: BtcEvent): string {
  const { type, payload } = event;

  if (type === "new_block") {
    const height = getPayloadNumber(payload, "height");
    return `Block #${height?.toLocaleString() ?? "?"}`;
  }
  if (type === "pending_mempool") {
    const txid = getPayloadString(payload, "txid");
    const feeRate = getPayloadNumber(payload, "fee_rate");
    return `${txid?.slice(0, 14) ?? "—"}… · ${feeRate ?? 0} sat/vB`;
  }
  if (type === "confirmed_tx") {
    const txid = getPayloadString(payload, "txid");
    const height = getPayloadNumber(payload, "height");
    return `${txid?.slice(0, 14) ?? "—"}… @ #${height?.toLocaleString() ?? "?"}`;
  }
  if (type === "mempool_replaced") {
    const txid = getPayloadString(payload, "replaced_txid");
    return `Replaced ${txid?.slice(0, 14) ?? "—"}…`;
  }
  if (type === "stream_requires_reregistration") {
    return "Watch addresses expired. Re-register to resume updates.";
  }
  return "Keep-alive heartbeat";
}

export function getBlockHashFromEvent(event: BtcEvent): string | null {
  if (event.type !== "new_block") return null;
  const hash = getPayloadString(event.payload, "hash");
  return hash && /^[0-9a-fA-F]{64}$/.test(hash) ? hash.toLowerCase() : null;
}

export function getFeedHeightStat(events: BtcEvent[]): string {
  for (const event of events) {
    const height = getPayloadNumber(event.payload, "height");
    if (height !== null) return `#${height.toLocaleString()}`;
  }
  return "—";
}

export function getMempoolStat(events: BtcEvent[]): string {
  const count = events.filter((e) => e.type === "pending_mempool").length;
  return count > 0 ? count.toLocaleString() : "—";
}
