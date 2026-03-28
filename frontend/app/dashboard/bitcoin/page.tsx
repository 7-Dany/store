import { redirect } from "next/navigation";

export const dynamic = "force-dynamic";

export default function BitcoinPage() {
  redirect("/dashboard/transaction-lifecycle");
}
