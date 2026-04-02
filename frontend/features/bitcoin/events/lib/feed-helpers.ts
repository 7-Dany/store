import { formatFeedEventTime } from "./feed-formatters";
import { CONN_CFG } from "./event-cfg";
import type { ConnState } from "@/features/bitcoin/types";

export function getLiveStat(
  connState: ConnState,
  lastHeartbeatAt: number | null,
  retryCount: number,
): string {
  if (lastHeartbeatAt !== null) return formatFeedEventTime(lastHeartbeatAt);
  if (connState === "reconnecting" && retryCount > 0)
    return `Retry · ${retryCount}`;
  const label = CONN_CFG[connState].label;
  return typeof label === "string" ? label : "—";
}
