import { apiClient } from "@/lib/api/http/client";

// ── Types ─────────────────────────────────────────────────────────────────────

export type BtcNetwork = "testnet4" | "mainnet";

export type TxStatus =
  | "confirmed"
  | "mempool"
  | "not_found"
  | "conflicting"
  | "abandoned";

export interface TxStatusResult {
  status: TxStatus;
  confirmations?: number;
  block_height?: number;
}

export interface TxStatusBatchResult {
  statuses: Record<string, TxStatusResult>;
}

export interface EventsStatusResult {
  zmq_connected: boolean;
  rpc_connected: boolean;
  active_connections: number;
  last_block_hash_age: number;
}

export interface WatchResult {
  watching: string[];
}

export interface BlockDetailsResult {
  hash: string;
  confirmations: number;
  height: number;
  version: number;
  merkle_root: string;
  time: number;
  median_time: number;
  nonce: number;
  bits: string;
  difficulty: number;
  chainwork: string;
  tx_count: number;
  previous_block_hash?: string;
  next_block_hash?: string;
}

// ── API functions ─────────────────────────────────────────────────────────────

/**
 * POST /bitcoin/events/token
 * Exchange the session JWT for a one-time HttpOnly SSE cookie (btc_sse_jti).
 * Must be called immediately before opening the EventSource.
 */
export async function issueSSEToken(): Promise<void> {
  await apiClient.post("/bitcoin/events/token");
}

/**
 * GET /bitcoin/events/status
 * Return the per-instance health snapshot for the SSE broker.
 */
export async function getEventsStatus(): Promise<EventsStatusResult> {
  const { data } = await apiClient.get<EventsStatusResult>(
    "/bitcoin/events/status",
  );
  return data;
}

/**
 * POST /bitcoin/watch
 * Register 1–20 Bitcoin addresses for real-time event monitoring.
 */
export async function watchAddresses(
  addresses: string[],
  network: BtcNetwork = "testnet4",
): Promise<WatchResult> {
  const { data } = await apiClient.post<WatchResult>("/bitcoin/watch", {
    addresses,
    network,
  });
  return data;
}

/**
 * GET /bitcoin/tx/{txid}/status
 * Look up the on-chain status of a single wallet transaction.
 */
export async function getTxStatus(txid: string): Promise<TxStatusResult> {
  const { data } = await apiClient.get<TxStatusResult>(
    `/bitcoin/tx/${encodeURIComponent(txid.toLowerCase())}/status`,
  );
  return data;
}

/**
 * GET /bitcoin/tx/status?ids=...
 * Batch-look up the on-chain status of up to 20 transactions.
 */
export async function getTxStatusBatch(
  txids: string[],
): Promise<TxStatusBatchResult> {
  const { data } = await apiClient.get<TxStatusBatchResult>(
    "/bitcoin/tx/status",
    { params: { ids: txids.map((t) => t.toLowerCase()).join(",") } },
  );
  return data;
}

/**
 * GET /bitcoin/block/{hash}
 * Look up block details for a canonical block hash.
 */
export async function getBlock(hash: string): Promise<BlockDetailsResult> {
  const { data } = await apiClient.get<BlockDetailsResult>(
    `/bitcoin/block/${encodeURIComponent(hash.toLowerCase())}`,
  );
  return data;
}
