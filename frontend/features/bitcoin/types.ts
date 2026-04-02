/**
 * Shared type definitions for the Bitcoin feature.
 * Single source of truth — imported by all components and hooks in this feature.
 */

export type BtcEventType =
  | "ping"
  | "new_block"
  | "pending_mempool"
  | "confirmed_tx"
  | "mempool_replaced"
  | "stream_requires_reregistration";

export type ConnState = "connecting" | "connected" | "reconnecting" | "error";

export interface BtcEvent {
  id: string;
  type: BtcEventType;
  payload: Record<string, unknown>;
  receivedAt: number;
}
