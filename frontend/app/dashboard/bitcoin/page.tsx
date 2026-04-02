// Permanent 308 redirect — no server runtime cost.
// next.config.ts handles this at build time via the `redirects` option, but
// keeping a lightweight page here preserves deep-link compat for any client
// that has the old URL bookmarked and lands before the CDN/edge cache is warm.
import { redirect } from "next/navigation";

export default function BitcoinPage() {
  redirect("/dashboard/transaction-lifecycle");
}
// No `export const dynamic` — Next.js 16 statically optimises this automatically.
