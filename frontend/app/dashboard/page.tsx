import {
  IconTrendingUp,
  IconTrendingDown,
  IconShoppingCart,
  IconUsers,
  IconPackage,
  IconCurrencyDollar,
  IconCircleFilled,
} from "@tabler/icons-react";
import { cn, capitalize } from "@/lib/utils";
import { redirect } from "next/navigation";

// ─── Mock data ────────────────────────────────────────────────────────────────

const STATS = [
  { label: "Total Revenue", value: "$48,295", change: "+12.5%", up: true,  sub: "vs last month",    icon: IconCurrencyDollar },
  { label: "Orders",        value: "1,284",   change: "+8.1%",  up: true,  sub: "vs last month",    icon: IconShoppingCart   },
  { label: "Customers",     value: "9,621",   change: "+3.4%",  up: true,  sub: "vs last month",    icon: IconUsers          },
  { label: "Products",      value: "342",     change: "-2 removed", up: false, sub: "active listings", icon: IconPackage     },
];

type OrderStatus = "Delivered" | "Processing" | "Shipped" | "Cancelled";

const ORDERS: {
  id: string; customer: string; product: string;
  date: string; amount: string; status: OrderStatus;
}[] = [
  { id: "#ORD-8821", customer: "Alice Martin",  product: "Wireless Headphones", date: "Mar 10, 2026", amount: "$129.00", status: "Delivered"  },
  { id: "#ORD-8820", customer: "James Lee",     product: "Mechanical Keyboard", date: "Mar 10, 2026", amount: "$189.00", status: "Processing" },
  { id: "#ORD-8819", customer: "Sofia Reyes",   product: "USB-C Hub (7-port)",  date: "Mar 9, 2026",  amount: "$64.00",  status: "Shipped"    },
  { id: "#ORD-8818", customer: "Noah Kim",      product: "Monitor Stand",       date: "Mar 9, 2026",  amount: "$49.00",  status: "Delivered"  },
  { id: "#ORD-8817", customer: "Emma Johnson",  product: "Laptop Sleeve 15\"",  date: "Mar 8, 2026",  amount: "$38.00",  status: "Cancelled"  },
  { id: "#ORD-8816", customer: "Liam Patel",    product: "Wireless Mouse",      date: "Mar 8, 2026",  amount: "$55.00",  status: "Delivered"  },
];

const TOP_PRODUCTS = [
  { name: "Wireless Headphones", sales: 284, revenue: "$36,636", stock: 41 },
  { name: "Mechanical Keyboard", sales: 197, revenue: "$37,233", stock: 12 },
  { name: "USB-C Hub (7-port)",  sales: 163, revenue: "$10,432", stock: 88 },
  { name: "Monitor Stand",       sales: 141, revenue: "$6,909",  stock: 56 },
  { name: "Wireless Mouse",      sales: 128, revenue: "$7,040",  stock: 33 },
];

const STATUS_STYLES: Record<OrderStatus, string> = {
  Delivered:  "bg-green-500/10 text-green-600 dark:text-green-400",
  Processing: "bg-primary/10 text-primary",
  Shipped:    "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  Cancelled:  "bg-destructive/10 text-destructive",
};

const STATUS_DOT: Record<OrderStatus, string> = {
  Delivered:  "text-green-500",
  Processing: "text-primary",
  Shipped:    "text-blue-500",
  Cancelled:  "text-destructive",
};

// ─── Page ─────────────────────────────────────────────────────────────────────

export default async function DashboardPage({
  searchParams,
}: {
  searchParams: Promise<{ provider?: string; action?: string }>;
}) {
  const { provider, action } = await searchParams;

  if (provider && action === "linked") {
    redirect(`/dashboard/profile?linked=${encodeURIComponent(provider)}`);
  }

  const justLoggedIn = !!provider;

  return (
    <div className="mx-auto flex max-w-300 flex-col gap-8 p-6 lg:p-8">

      {/* ── Header ── */}
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Overview</h1>
        <p className="mt-0.5 text-sm text-muted-foreground">
          {justLoggedIn
            ? `Signed in with ${capitalize(provider!)}.`
            : "Here's what's happening in your store today."}
        </p>
      </div>

      {/* ── Stats ── */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {STATS.map((s) => (
          <div
            key={s.label}
            className="flex flex-col gap-3 rounded-2xl border border-border bg-card p-5 shadow-xs"
          >
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-muted-foreground">{s.label}</span>
              <div className="flex size-8 items-center justify-center rounded-lg bg-primary/10">
                <s.icon size={16} stroke={1.75} className="text-primary" />
              </div>
            </div>
            <div>
              <p className="text-2xl font-semibold tracking-tight text-foreground">{s.value}</p>
              <div className="mt-1 flex items-center gap-1.5">
                {s.up
                  ? <IconTrendingUp size={13} stroke={2} className="text-green-500" />
                  : <IconTrendingDown size={13} stroke={2} className="text-muted-foreground" />}
                <span className={cn("text-xs font-medium", s.up ? "text-green-600 dark:text-green-400" : "text-muted-foreground")}>
                  {s.change}
                </span>
                <span className="text-xs text-muted-foreground">{s.sub}</span>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* ── Content ── */}
      <div className="grid gap-6 lg:grid-cols-[1fr_auto]">

        {/* Orders table */}
        <div className="flex flex-col gap-4 overflow-hidden rounded-2xl border border-border bg-card shadow-xs">
          <div className="flex items-center justify-between px-5 pt-5">
            <div>
              <h2 className="text-sm font-semibold text-foreground">Recent Orders</h2>
              <p className="text-xs text-muted-foreground">Last 6 transactions</p>
            </div>
            <button className="text-xs font-medium text-primary transition-opacity hover:opacity-75">
              View all
            </button>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-y border-border bg-muted/40">
                  {["Order", "Customer", "Product", "Date", "Amount", "Status"].map((h) => (
                    <th
                      key={h}
                      className="whitespace-nowrap px-5 py-2.5 text-left text-xs font-medium text-muted-foreground"
                    >
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {ORDERS.map((o, i) => (
                  <tr
                    key={o.id}
                    className={cn(
                      "transition-colors hover:bg-muted/30",
                      i !== ORDERS.length - 1 && "border-b border-border/60",
                    )}
                  >
                    <td className="whitespace-nowrap px-5 py-3 font-mono text-xs text-muted-foreground">{o.id}</td>
                    <td className="whitespace-nowrap px-5 py-3 font-medium text-foreground">{o.customer}</td>
                    <td className="whitespace-nowrap px-5 py-3 text-muted-foreground">{o.product}</td>
                    <td className="whitespace-nowrap px-5 py-3 text-muted-foreground">{o.date}</td>
                    <td className="whitespace-nowrap px-5 py-3 font-medium text-foreground">{o.amount}</td>
                    <td className="whitespace-nowrap px-5 py-3">
                      <span
                        className={cn(
                          "inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium",
                          STATUS_STYLES[o.status],
                        )}
                      >
                        <IconCircleFilled size={6} className={STATUS_DOT[o.status]} />
                        {o.status}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        {/* Top products */}
        <div className="flex w-full flex-col gap-4 rounded-2xl border border-border bg-card p-5 shadow-xs lg:w-72">
          <div>
            <h2 className="text-sm font-semibold text-foreground">Top Products</h2>
            <p className="text-xs text-muted-foreground">By units sold this month</p>
          </div>
          <div className="flex flex-col gap-3">
            {TOP_PRODUCTS.map((p, i) => (
              <div key={p.name} className="flex flex-col gap-1.5">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="w-4 shrink-0 font-mono text-xs text-muted-foreground">{i + 1}</span>
                    <span className="truncate text-sm font-medium text-foreground">{p.name}</span>
                  </div>
                  <span className="shrink-0 text-xs text-muted-foreground">{p.sales} sold</span>
                </div>
                <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-primary/60"
                    style={{ width: `${Math.round((p.sales / TOP_PRODUCTS[0].sales) * 100)}%` }}
                  />
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-xs text-muted-foreground">{p.revenue} revenue</span>
                  <span className={cn(
                    "text-xs font-medium",
                    p.stock < 20 ? "text-destructive" : "text-muted-foreground",
                  )}>
                    {p.stock} in stock{p.stock < 20 && " ⚠"}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
