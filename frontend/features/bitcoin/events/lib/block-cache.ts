/**
 * Block-details request cache with TTL expiry and a bounded eviction strategy.
 *
 * Three cooperating Maps:
 *  - blockDetailsCache        — successful results; TTL 5 min
 *  - blockDetailsInflight     — in-progress promises; deduplicated
 *  - blockDetailsErrorCache   — recent errors; TTL 30 s (prevents retry spam)
 *
 * Eviction: oldest-inserted entry is dropped when either result cache exceeds
 * MAX_CACHE_SIZE. Maps preserve insertion order, so the first key is always
 * the oldest entry.
 */

import { getBlock } from "@/lib/api/bitcoin";
import type { BlockDetailsResult } from "@/lib/api/bitcoin";

const BLOCK_DETAILS_TTL_MS = 5 * 60 * 1_000;
const BLOCK_ERROR_TTL_MS = 30 * 1_000;
const MAX_CACHE_SIZE = 200;

const blockDetailsCache = new Map<
  string,
  { data: BlockDetailsResult; expiresAt: number }
>();
const blockDetailsInflight = new Map<string, Promise<BlockDetailsResult>>();
const blockDetailsErrorCache = new Map<
  string,
  { message: string; expiresAt: number }
>();

function evictOldest(cache: Map<string, unknown>): void {
  if (cache.size > MAX_CACHE_SIZE) {
    const firstKey = cache.keys().next().value;
    if (firstKey !== undefined) cache.delete(firstKey);
  }
}

export async function getBlockDetailsCached(
  hash: string,
): Promise<BlockDetailsResult> {
  const key = hash.toLowerCase();
  const now = Date.now();

  const cached = blockDetailsCache.get(key);
  if (cached && cached.expiresAt > now) return cached.data;

  const cachedError = blockDetailsErrorCache.get(key);
  if (cachedError && cachedError.expiresAt > now)
    throw new Error(cachedError.message);

  const inflight = blockDetailsInflight.get(key);
  if (inflight) return inflight;

  const request = getBlock(key)
    .then((data) => {
      evictOldest(blockDetailsCache);
      blockDetailsCache.set(key, {
        data,
        expiresAt: Date.now() + BLOCK_DETAILS_TTL_MS,
      });
      blockDetailsErrorCache.delete(key);
      return data;
    })
    .catch((err: unknown) => {
      const message =
        err instanceof Error
          ? err.message
          : "Failed to load block details. Try again.";
      evictOldest(blockDetailsErrorCache);
      blockDetailsErrorCache.set(key, {
        message,
        expiresAt: Date.now() + BLOCK_ERROR_TTL_MS,
      });
      throw new Error(message);
    })
    .finally(() => {
      blockDetailsInflight.delete(key);
    });

  blockDetailsInflight.set(key, request);
  return request;
}
