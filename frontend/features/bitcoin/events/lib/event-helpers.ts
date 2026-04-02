import { getPayloadNumber } from "./feed-formatters";
import type { BtcEvent } from "@/features/bitcoin/types";

export function getHeading(event: BtcEvent): string {
  switch (event.type) {
    case "new_block":
      return `Block #${getPayloadNumber(event.payload, "height")?.toLocaleString() ?? "?"}`;
    case "pending_mempool":
      return "Mempool event";
    case "confirmed_tx":
      return "Confirmed tx";
    case "mempool_replaced":
      return "Replacement";
    case "stream_requires_reregistration":
      return "Re-registration";
    default:
      return "Heartbeat";
  }
}
