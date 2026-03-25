"use client";
import { useState, useMemo, useEffect } from "react";
import {
  IconCheck,
  IconLock,
  IconCircleCheck,
} from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  CardAction,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// ─── Tag colours ──────────────────────────────────────────────────────────────

const TAG_COLOR: Record<string, { bg: string; text: string; ring: string }> = {
  Platform:   { bg: "bg-blue-500/10",    text: "text-blue-600 dark:text-blue-400",    ring: "ring-blue-500/25"  },
  Schema:     { bg: "bg-zinc-500/10",    text: "text-zinc-600 dark:text-zinc-400",    ring: "ring-zinc-500/25"  },
  Infra:      { bg: "bg-emerald-500/10", text: "text-emerald-600 dark:text-emerald-400", ring: "ring-emerald-500/25" },
  "Stage 0":  { bg: "bg-emerald-500/10", text: "text-emerald-600 dark:text-emerald-400", ring: "ring-emerald-500/25" },
  "Stage 2":  { bg: "bg-amber-500/10",   text: "text-amber-600 dark:text-amber-400",  ring: "ring-amber-500/25" },
  "Stage 2a": { bg: "bg-yellow-500/10",  text: "text-yellow-600 dark:text-yellow-500",ring: "ring-yellow-500/25"},
  "Stage 2b": { bg: "bg-orange-500/10",  text: "text-orange-600 dark:text-orange-400",ring: "ring-orange-500/25"},
  "Stage 2c": { bg: "bg-red-500/10",     text: "text-red-600 dark:text-red-400",      ring: "ring-red-500/25"  },
  Compliance: { bg: "bg-rose-500/10",    text: "text-rose-600 dark:text-rose-400",    ring: "ring-rose-500/25"  },
  Workers:    { bg: "bg-violet-500/10",  text: "text-violet-600 dark:text-violet-400",ring: "ring-violet-500/25"},
  Wiring:     { bg: "bg-zinc-500/10",    text: "text-zinc-600 dark:text-zinc-400",    ring: "ring-zinc-500/25" },
};

const SYSTEM_STYLE: Record<string, { dot: string; label: string }> = {
  btc: { dot: "bg-amber-400",  label: "bitcoin/"  },
  jq:  { dot: "bg-violet-400", label: "jobqueue/" },
};

// ─── Types ────────────────────────────────────────────────────────────────────

type Pkg = {
  name: string;
  tag: string;
  desc: string;
  deps: string[];
  system: "btc" | "jq";
};

type Phase = {
  num: number;
  title: string;
  parallel: boolean;
  note: string;
  pkgs: Pkg[];
};

// ─── Unified build tree ───────────────────────────────────────────────────────

const PHASES: Phase[] = [
  {
    num: 1,
    title: "All foundations",
    parallel: true,
    note: "All six are fully independent — start any or all simultaneously. btc-schema/ is a design task: write the complete DDL for every BTC table before any package that owns one is implemented.",
    pkgs: [
      {
        name: "rpc/",
        system: "btc",
        tag: "Platform",
        desc: "Bitcoin Core HTTP client. GetRawTransaction, GetBlockHeader, GetBlock, GetBlockHash, GetBlockCount, GetBlockchainInfo. BtcToSat precision safety (math.Round, not *1e8). Startup chain match + txindex checks.",
        deps: [],
      },
      {
        name: "zmq/",
        system: "btc",
        tag: "Platform",
        desc: "ZMQ subscriber. 20-block + 20-tx worker pool. BlockEvent, TxEvent, RecoveryEvent with HashHex() byte-order reversal. safeInvoke with per-handler timeout, panic recovery, and wg drain (30s ceiling on Shutdown).",
        deps: [],
      },
      {
        name: "prerequisites/",
        system: "btc",
        tag: "Infra",
        desc: "Config vars (BTC_*), audit event constants, kvstore interface extensions (RefreshTTL, AtomicCounterStore, ListStore, PubSubStore), ConnectionCounter, RBAC PermBitcoin* constants, app.Deps new fields, token.Sign primitive.",
        deps: [],
      },
      {
        name: "btc-schema/",
        system: "btc",
        tag: "Schema",
        desc: "Full DDL for every BTC-owned table: invoices, invoice_addresses, invoice_address_monitoring, invoice_payments, btc_outage_log, bitcoin_sync_state, bitcoin_block_history (Gap F — undefined, must resolve), vendor_balances, payout_records, financial_audit_events, reconciliation_job_state, reconciliation_run_history, platform_config, wallet_backup_success. New tables from added packages: dispute_records (dispute state machine + payout freeze), kyc_submissions + btc_kyc_status ENUM on payout_records, fatf_travel_rule_records + fn_fatf_address_consistency trigger (compliance), gdpr_erasure_requests, sse_token_issuances, webhook_deliveries (idx_wd_pending partial index). Also covers cross-table read dependencies, FK references, index coverage, and the hd_derivation_index column type (Gap: INTEGER vs BIGINT). Must be approved before any owning package writes migration files.",
        deps: [],
      },
      {
        name: "006_jobqueue.sql",
        system: "jq",
        tag: "Schema",
        desc: "Creates jobs, workers, job_schedules, job_paused_kinds. Drops request_executions (replaced by kind=execute_request jobs). Strips delivery_attempts, last_attempt_at, delivery_error from request_notifications. Gate: 4 tables present, request_executions gone, 0 delivery columns.",
        deps: [],
      },
      {
        name: "kvstore Publish/Subscribe",
        system: "jq",
        tag: "Platform",
        desc: "Add Publish() and Subscribe() to RedisStore. Subscribe spawns a goroutine that reads go-redis PubSub and forwards to a chan string (closed on ctx cancel). Gate: RunPubSubContractTests green — T-C1 through T-C5.",
        deps: [],
      },
    ],
  },

  {
    num: 2,
    title: "BTC Stage 0 domain + JQ core types",
    parallel: true,
    note: "BTC Stage 0 packages each need one or two Phase 1 BTC packages. audit/, rate/, wallet-backup/ additionally need btc-schema/ approved. JQ core needs both Phase 1 JQ packages.",
    pkgs: [
      {
        name: "txstatus/",
        system: "btc",
        tag: "Stage 0",
        desc: "Single GET /tx/:txid/status and batch GET /tx/status?ids= (up to 20). Display-only. Fresh RPC call per request, no caching, no settlement side effects. No DB tables owned.",
        deps: ["rpc/"],
      },
      {
        name: "watch/",
        system: "btc",
        tag: "Stage 0",
        desc: "In-memory sync.Map watch list per userID. SSE auth token (one-time JTI, HttpOnly cookie, sid HMAC, IPv4 /24 subnet binding). Redis ConnectionCounter for per-user SSE cap with Heartbeat every 2 min. No DB tables owned.",
        deps: ["rpc/", "zmq/"],
      },
      {
        name: "events/",
        system: "btc",
        tag: "Stage 0",
        desc: "SSE stream (display-only, best-effort). pendingMempool cap (default 10 000). spentOutpoints secondary index for RBF detection. confirmed_tx fan-out on BlockEvent. Reconnect reconciliation via txstatus. No DB tables owned.",
        deps: ["rpc/", "zmq/"],
      },
      {
        name: "audit/",
        system: "btc",
        tag: "Stage 2",
        desc: "Owns: financial_audit_events (append-only, INSERT-only DB grant, UPDATE/DELETE rejected by trigger), reconciliation_job_state, platform_config. Write interface used by every financial package. Required fields: actor, satoshi_amounts, fiat_equivalent, invoice_id or payout_record_id.",
        deps: ["prerequisites/", "btc-schema/"],
      },
      {
        name: "rate/",
        system: "btc",
        tag: "Stage 2",
        desc: "BTC/fiat rate cache, deviation policy, stale subscription debit deferral. debit_defer_count + debit_first_deferred_at must be persisted (not in-memory) to survive restarts — Gap C fix. Needs btc-schema/ for the rate persistence table design.",
        deps: ["rpc/", "prerequisites/", "btc-schema/"],
      },
      {
        name: "wallet-backup/",
        system: "btc",
        tag: "Stage 2",
        desc: "Owns: wallet_backup_success (written only after backup file is copied to storage; alert triggers on this timestamp, not job run time). Backup layers A/B/C/D. Recovery scenarios. Keypool cursor advance. Pre-mainnet checklist.",
        deps: ["rpc/", "prerequisites/", "btc-schema/"],
      },
      {
        name: "job.go · store.go · metrics.go",
        system: "jq",
        tag: "Platform",
        desc: "All interfaces: Handler, HandlerFunc, Submitter, PubSub, MetricsRecorder, JobStore. pgJobStore with aging claim query (effective_priority = priority + LEAST(minutes_waited, AgingCap)). QueryMetricsRecorder (V1: SQL on each scrape) + NoopMetricsRecorder. Gate: T-26–T-32.",
        deps: ["006_jobqueue.sql", "kvstore Publish/Subscribe"],
      },
    ],
  },

  {
    num: 3,
    title: "BTC vendor + JQ dispatcher & worker handlers",
    parallel: true,
    note: "vendor/ needs audit/ first. JQ dispatcher and worker handlers both only need Phase 2 JQ core — run all three in parallel.",
    pkgs: [
      {
        name: "vendor/",
        system: "btc",
        tag: "Stage 2",
        desc: "Tier config, vendor lifecycle, TOTP step-up auth (Redis session, 15-min TTL, not in-memory — Gap E fix). Regulatory context. Tier assignment changes vendor RBAC role. invoice/ needs vendor wallet mode at invoice creation time. No DB tables owned.",
        deps: ["prerequisites/", "audit/"],
      },
      {
        name: "dispatcher · scheduler · stall · manager",
        system: "jq",
        tag: "Platform",
        desc: "N worker goroutines each with Redis SUBSCRIBE wake + 10s fallback ticker. SKIP LOCKED claim. ScheduleWatcher: single goroutine, 10s poll, cron via robfig/cron + interval. StallDetector: every 30s. Manager.Register panics if post-Start or duplicate. Gate: T-01–T-25.",
        deps: ["job.go · store.go · metrics.go"],
      },
      {
        name: "worker handlers",
        system: "jq",
        tag: "Workers",
        desc: "kinds.go: add KindExecuteRequest, KindSendNotification, KindPurgeCompleted, KindPurgeExpiredPermissions. execute_request.go replaces request_executions inline execution. send_notification.go replaces delivery retry loop. purge_completed.go. Gate: T-41–T-46.",
        deps: ["job.go · store.go · metrics.go"],
      },
    ],
  },

  {
    num: 4,
    title: "BTC invoice, compliance + JQ admin API",
    parallel: true,
    note: "invoice/ and compliance/ both become available after Phase 3. compliance/ must land before sweep/ (Phase 5) because SweepService.constructAndBroadcast inserts FATF records before every broadcast. JQ admin API needs the dispatcher — run all three in parallel.",
    pkgs: [
      {
        name: "invoice/",
        system: "btc",
        tag: "Stage 2a",
        desc: "Owns: invoices, invoice_addresses (hd_derivation_index column type from btc-schema/), invoice_address_monitoring (Contract 7 — authoritative ZMQ watch list), invoice_payments (UNIQUE (txid, vout_index) idempotency constraint). Two-step RPC address creation. RegisterImmediate on ZMQ before returning response (Contract 1). Expiry formula uses btc_outage_log. Gap B: buyer refund address ismine check.",
        deps: ["zmq/", "rpc/", "vendor/", "audit/", "prerequisites/", "btc-schema/"],
      },
      {
        name: "compliance/",
        system: "btc",
        tag: "Compliance",
        desc: "FATF Travel Rule recording — InsertFATFRecord called inside SweepService.constructAndBroadcast before each payout transitions to broadcast. fn_fatf_address_consistency BEFORE INSERT trigger enforces beneficiary_address == payout destination at DB level (irrecoverable compliance failure if wrong). GDPR erasure job: crash-safe via tables_processed append-only array checkpoint; each step idempotent. HMAC actor labels for financial_audit_events — app.audit_hmac_secret session variable must be set before INSERT. IP pseudonymisation for SSE tokens with daily rotation key stored in secrets manager (not DB).",
        deps: ["audit/", "vendor/", "btc-schema/"],
      },
      {
        name: "api.go · ws.go",
        system: "jq",
        tag: "Platform",
        desc: "20 REST endpoints across workers, jobs, dead-letter, queues, schedules, stats, metrics. GET /metrics delegates to metrics.MetricsHandler() — zero logic in api.go. WSHub: 64-event buffered channel per client, hub goroutine never blocks on network I/O. Gate: T-33–T-40, T-47–T-49.",
        deps: ["dispatcher · scheduler · stall · manager"],
      },
    ],
  },

  {
    num: 5,
    title: "BTC payment + sweep + JQ server wiring",
    parallel: true,
    note: "payment/ and sweep/ are independent of each other. JQ server wiring needs dispatcher + API + worker handlers. Wire JQ before settlement — settlement submits jobs at runtime.",
    pkgs: [
      {
        name: "payment/",
        system: "btc",
        tag: "Stage 2b",
        desc: "Owns: btc_outage_log (written on node disconnect/reconnect; read by invoice expiry formula and resilience HandleRecovery). TX detection via ZMQ subscriber. Confirmation depths. Mempool drop watchdog. On reconnect: closes outage log AND triggers resilience.HandleRecovery atomically (Contract 3).",
        deps: ["invoice/", "audit/", "prerequisites/", "btc-schema/"],
      },
      {
        name: "sweep/",
        system: "btc",
        tag: "Stage 2c",
        desc: "Full PSBT flow: walletcreatefundedpsbt → walletprocesspsbt → finalizepsbt → DB commit (constructing→broadcast + txid) → sendrawtransaction with maxfeerate guard. DB-before-network is a hard invariant. Batch up to 100 outputs. 3-block confirmation. BTC_AUTO_SWEEP_ENABLED=false default. No tables owned — writes into settlement's payout_records. Calls compliance.InsertFATFRecord before broadcast for payouts above FATF threshold (fn_fatf_address_consistency trigger enforces address match at DB level).",
        deps: ["rpc/", "audit/", "prerequisites/", "btc-schema/", "compliance/"],
      },
      {
        name: "server · deps · config",
        system: "jq",
        tag: "Wiring",
        desc: "config.go: JobWorkers int, JobRetentionDays int. deps.go: Jobs jobqueue.Submitter, JobMgr *jobqueue.Manager. server.go: NewManager, Register all four handlers, EnsureSchedule for purge_accounts_hourly + purge_completed_jobs_daily, mount /admin/jobqueue. Cleanup: mgr.Shutdown → q.Shutdown → pool.Close. Removes PurgeWorker goroutine.",
        deps: [
          "dispatcher · scheduler · stall · manager",
          "api.go · ws.go",
          "worker handlers",
        ],
      },
    ],
  },

  {
    num: 6,
    title: "Settlement",
    parallel: false,
    note: "Gate: bitcoin_balance_drift_satoshis = 0 for ≥ 1 week on testnet4. Requires JQ fully wired — settlement submits sweep, reconciliation, and monitoring jobs at runtime.",
    pkgs: [
      {
        name: "settlement/",
        system: "btc",
        tag: "Stage 2b",
        desc: "Owns: vendor_balances (CHECK balance_satoshis >= 0; SELECT FOR UPDATE before every read-modify-write), payout_records (BEFORE INSERT trigger rejects when parent invoice status != 'settled'). Settlement phases, underpay/overpay/hybrid, atomic payout state machine. Calls sweep.SweepService (Contract 4). Calls rate for subscription debits (Contract 8). Phase 1 tolerance check BEFORE settling claim.",
        deps: ["invoice/", "payment/", "rate/", "sweep/", "audit/", "btc-schema/", "server · deps · config"],
      },
    ],
  },

  {
    num: 7,
    title: "Post-settlement features",
    parallel: true,
    note: "All five packages depend on settlement/ being stable but are fully independent of each other — start all in parallel. Gate for reconciliation/: bitcoin_balance_drift_satoshis = 0 for ≥ 1 week on testnet4. Gate for resilience/: sweep stable on testnet4 (real Bitcoin moves after validation).",
    pkgs: [
      {
        name: "resilience/",
        system: "btc",
        tag: "Stage 2c",
        desc: "Owns: bitcoin_sync_state (last_processed_height per network; sentinel -1 = never processed), bitcoin_block_history (Gap F — schema undefined in design docs; must resolve before starting this package). Degraded mode detection. Reorg rollback via settlement.rollbackSettlementFromHeight (Contract 5). Post-outage backfill from last_processed_height cursor. HandleRecovery triggered by payment on reconnect.",
        deps: ["payment/", "settlement/", "btc-schema/"],
      },
      {
        name: "reconciliation/",
        system: "btc",
        tag: "Stage 2",
        desc: "6-hour scheduled job via platform job queue (unique_job=true). Advisory lock (pg_try_advisory_lock per network) prevents concurrent runs — second instance logs INFO and exits. reconcileSegment processes BTC_RECONCILIATION_CHECKPOINT_INTERVAL blocks (default 100) per TX with SET LOCAL lock_timeout='30s' to avoid blocking real-time ZMQ settlement. UpsertInvoicePayment uses ON CONFLICT DO NOTHING for ZMQ idempotency. Formula: on-chain confirmed UTXOs = inflight invoices + inflight payout records + platform vendor balances + treasury_reserve_satoshis. Discrepancy → SetSweepHold + CRITICAL alert. fn_ops_audit_platform_config trigger fires on hold set/clear. Staleness alert after 8h with no successful run. Owns: reconciliation_job_state, reconciliation_run_history (append-only, never deleted).",
        deps: ["rpc/", "audit/", "settlement/", "sweep/", "btc-schema/", "server · deps · config"],
      },
      {
        name: "webhook/",
        system: "btc",
        tag: "Platform",
        desc: "Outbox pattern: InsertWebhookDelivery MUST be called inside the same DB transaction as the state change that triggers the event (invoice.settled, etc.) — rollback removes both atomically. Delivery worker polls every BTC_WEBHOOK_POLL_INTERVAL_MS (default 5s), up to 20 concurrent workers via semaphore. 7-step retry schedule: 30s → 2m → 10m → 30m → 2h → 6h → 24h, ±15% jitter. HMAC-SHA256 signature header (X-Platform-Signature: sha256=<hex>); timing-safe comparison required on vendor side. Dead letter review: admin retry (resets attempt_count=0) or abandon (terminal). Automated vendor webhook suspension after 100 dead letters in 7-day rolling window. Owns: webhook_deliveries.",
        deps: ["invoice/", "settlement/", "vendor/", "audit/"],
      },
      {
        name: "kyc/",
        system: "btc",
        tag: "Compliance",
        desc: "KYC submission state machine: submitted → under_review → approved/rejected; approved → expired (background job). Payout gate at record creation: if net_sat ≥ tier KycCheckRequiredAtThresholdSatoshis and vendor lacks approved KYC → kyc_status=required + triggerKYCSubmission. Held → queued promotion blocked per-record for required/pending KYC (approved records for same vendor still promoted). Provider webhook at POST /api/v1/admin/kyc/webhook: timing-safe signature check, optimistic-lock transition (WHERE status=$expected), idempotent on duplicate delivery (0 RowsAffected → 200 OK). Approval triggers bulk UPDATE payout_records in same TX as submission update. Re-submission cooldown + 3-rejection escalation to admin CRITICAL alert. Owns: kyc_submissions, btc_kyc_status ENUM on payout_records.",
        deps: ["settlement/", "audit/", "btc-schema/"],
      },
      {
        name: "dispute/",
        system: "btc",
        tag: "Compliance",
        desc: "Dispute state machine: open → awaiting_vendor/awaiting_buyer → resolved_vendor/resolved_buyer/resolved_platform/withdrawn/escalated (all terminal). On open: UPDATE queued payout records → held with dispute_hold; sweep abort guard checks dispute_id IS NOT NULL at broadcast boundary. On resolved_vendor/withdrawn: unfreeze held → queued. On resolved_buyer + held: cancel payouts + create refund payout record; on resolved_buyer + already broadcast/confirmed → resolved_platform + financial_audit_event for platform loss. Auto-resolution: daily job transitions awaiting_vendor where vendor_deadline < NOW() → resolved_buyer with actor_type='system'. All terminal transitions require TOTP step-up. Every transition writes financial_audit_events. Owns: dispute_records.",
        deps: ["invoice/", "settlement/", "audit/"],
      },
    ],
  },
];

// ─── Status logic ─────────────────────────────────────────────────────────────

type Status = "done" | "available" | "locked";

function getStatus(pkg: Pkg, done: Set<string>): Status {
  if (done.has(pkg.name)) return "done";
  if (pkg.deps.every((d) => done.has(d))) return "available";
  return "locked";
}

// ─── Components ───────────────────────────────────────────────────────────────

function TagBadge({ label }: { label: string }) {
  const c = TAG_COLOR[label] ?? { bg: "bg-muted", text: "text-muted-foreground", ring: "ring-border" };
  return (
    <span className={cn("inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ring-1", c.bg, c.text, c.ring)}>
      {label}
    </span>
  );
}

function SystemDot({ system }: { system: "btc" | "jq" }) {
  const s = SYSTEM_STYLE[system];
  return (
    <span className="inline-flex items-center gap-1 text-[10px] text-muted-foreground/60">
      <span className={cn("inline-block size-1.5 rounded-full", s.dot)} />
      {s.label}
    </span>
  );
}

function DepChip({ text, satisfied }: { text: string; satisfied: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] ring-1 transition-all",
        satisfied
          ? "text-muted-foreground/40 line-through ring-border/25"
          : "text-muted-foreground ring-border/60"
      )}
    >
      {satisfied && <IconCheck className="size-2.5 shrink-0" />}
      {text}
    </span>
  );
}

function PkgCard({
  pkg,
  status,
  onToggle,
  doneSet,
}: {
  pkg: Pkg;
  status: Status;
  onToggle: () => void;
  doneSet: Set<string>;
}) {
  const blockedCount = pkg.deps.filter((d) => !doneSet.has(d)).length;

  return (
    <Card
      size="sm"
      className={cn(
        "flex-1 basis-52 min-w-48 transition-all duration-200 relative overflow-hidden",
        pkg.system === "btc"
          ? "before:absolute before:inset-x-0 before:top-0 before:h-[2px] before:bg-amber-400/60"
          : "before:absolute before:inset-x-0 before:top-0 before:h-[2px] before:bg-violet-400/60",
        status === "done" && "opacity-50",
        status === "locked" && "opacity-30 saturate-0 pointer-events-none"
      )}
    >
      <CardHeader className="border-b pb-3!">
        <div className="flex items-start justify-between gap-2">
          <div className="flex flex-col gap-1.5 min-w-0">
            <CardTitle className="font-mono text-xs font-semibold leading-snug">
              <span className={cn(status === "done" && "line-through opacity-60")}>
                {pkg.name}
              </span>
            </CardTitle>
            <div className="flex items-center gap-2 flex-wrap">
              <TagBadge label={pkg.tag} />
              <SystemDot system={pkg.system} />
            </div>
          </div>
          <CardAction>
            {status === "locked" ? (
              <IconLock className="size-3.5 text-muted-foreground/30 mt-0.5" />
            ) : (
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={onToggle}
                className={cn(
                  "rounded-md transition-colors",
                  status === "done"
                    ? "text-emerald-500 hover:text-emerald-600 hover:bg-emerald-500/10"
                    : "text-muted-foreground/30 hover:text-foreground hover:bg-muted"
                )}
                aria-label={status === "done" ? "Mark undone" : "Mark done"}
              >
                <IconCheck className="size-3.5" />
              </Button>
            )}
          </CardAction>
        </div>
      </CardHeader>

      <CardContent className="pt-3 flex flex-col gap-3">
        {status !== "locked" ? (
          <p className="text-[11.5px] leading-relaxed text-muted-foreground">{pkg.desc}</p>
        ) : (
          <p className="text-[11px] italic text-muted-foreground/50">
            Waiting on {blockedCount} dep{blockedCount !== 1 ? "s" : ""}
          </p>
        )}

        {pkg.deps.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {pkg.deps.map((d) => (
              <DepChip key={d} text={d} satisfied={doneSet.has(d)} />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function PhaseRow({
  phase,
  done,
  onToggle,
  isLast,
}: {
  phase: Phase;
  done: Set<string>;
  onToggle: (name: string) => void;
  isLast: boolean;
}) {
  const statuses = phase.pkgs.map((p) => getStatus(p, done));
  const doneCount = statuses.filter((s) => s === "done").length;
  const total = phase.pkgs.length;
  const allDone = doneCount === total;
  const anyUnlocked = statuses.some((s) => s !== "locked");
  const systems = [...new Set(phase.pkgs.map((p) => p.system))];

  return (
    <div className="flex gap-4">
      {/* Rail */}
      <div className="flex flex-col items-center shrink-0 w-7">
        <div
          className={cn(
            "flex size-7 shrink-0 items-center justify-center rounded-full ring-1 text-xs font-semibold transition-all duration-300 mt-0.5",
            allDone
              ? "bg-emerald-500/15 ring-emerald-500/40 text-emerald-500"
              : anyUnlocked
              ? "bg-muted ring-border text-muted-foreground"
              : "bg-muted/40 ring-border/30 text-muted-foreground/40"
          )}
        >
          {allDone ? <IconCircleCheck className="size-4" /> : <span>{phase.num}</span>}
        </div>
        {!isLast && (
          <div
            className={cn(
              "mt-1 w-px flex-1 min-h-5 rounded-full transition-colors duration-300",
              anyUnlocked ? "bg-border" : "bg-border/25"
            )}
          />
        )}
      </div>

      {/* Content */}
      <div className="flex-1 min-w-0 pb-7">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <span className="text-sm font-semibold">{phase.title}</span>

          {phase.parallel && phase.pkgs.length > 1 && (
            <Badge variant="outline" className="text-[10px] h-4 px-1.5">parallel</Badge>
          )}

          <Badge
            variant="outline"
            className={cn(
              "text-[10px] h-4 px-1.5 font-mono transition-colors",
              allDone && "border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
            )}
          >
            {doneCount}/{total}
          </Badge>

          <div className="ml-auto flex items-center gap-2">
            {systems.map((sys) => (
              <span key={sys} className="inline-flex items-center gap-1 text-[10px] text-muted-foreground/50">
                <span className={cn("inline-block size-1.5 rounded-full", SYSTEM_STYLE[sys].dot)} />
                {SYSTEM_STYLE[sys].label}
              </span>
            ))}
          </div>
        </div>

        <p className="mb-3 text-[11.5px] leading-relaxed text-muted-foreground">{phase.note}</p>

        <div className="flex flex-wrap gap-2">
          {phase.pkgs.map((p, i) => (
            <PkgCard
              key={p.name}
              pkg={p}
              status={statuses[i]}
              onToggle={() => onToggle(p.name)}
              doneSet={done}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function StagePage() {
  const [done, setDone] = useState<Set<string>>(new Set<string>());
  const [hydrated, setHydrated] = useState(false);

  // Load persisted state after mount to avoid SSR/client hydration mismatch
  useEffect(() => {
    try {
      const saved = localStorage.getItem("stage-tracker-done");
      if (saved) setDone(new Set<string>(JSON.parse(saved)));
    } catch {
      // ignore corrupt storage
    }
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (!hydrated) return;
    localStorage.setItem("stage-tracker-done", JSON.stringify([...done]));
  }, [done, hydrated]);

  const allPkgs  = useMemo(() => PHASES.flatMap((ph) => ph.pkgs), []);
  const btcPkgs  = useMemo(() => allPkgs.filter((p) => p.system === "btc"), [allPkgs]);
  const jqPkgs   = useMemo(() => allPkgs.filter((p) => p.system === "jq"),  [allPkgs]);

  const btcDone   = btcPkgs.filter((p) => done.has(p.name)).length;
  const jqDone    = jqPkgs.filter((p) => done.has(p.name)).length;
  const totalDone = done.size;
  const totalPkgs = allPkgs.length;
  const totalPct  = totalPkgs ? (totalDone / totalPkgs) * 100 : 0;

  function toggle(name: string) {
    setDone((prev) => {
      const next = new Set(prev);
      if (next.has(name)) {
        next.delete(name);
        let changed = true;
        while (changed) {
          changed = false;
          for (const p of allPkgs) {
            if (next.has(p.name) && p.deps.some((d) => !next.has(d))) {
              next.delete(p.name);
              changed = true;
            }
          }
        }
      } else {
        next.add(name);
      }
      return next;
    });
  }

  return (
    <div className="p-5 pb-12 max-w-4xl mx-auto">
      {/* Header */}
      <div className="mb-6 flex flex-col gap-3">
        <div className="flex items-start justify-between gap-4 flex-wrap">
          <div>
            <h1 className="text-base font-semibold">Build tracker</h1>
            <p className="text-xs text-muted-foreground mt-0.5">
              Mark a package done to unlock downstream work. Unchecking cascades automatically.
            </p>
          </div>
          <div className="flex items-center gap-4 text-[11px] text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-block size-2 rounded-full bg-amber-400" />
              bitcoin/
              <span className="font-mono text-muted-foreground/50">{btcDone}/{btcPkgs.length}</span>
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-block size-2 rounded-full bg-violet-400" />
              jobqueue/
              <span className="font-mono text-muted-foreground/50">{jqDone}/{jqPkgs.length}</span>
            </span>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
            <div
              className="h-full rounded-full bg-emerald-500 transition-all duration-500 ease-out"
              style={{ width: `${totalPct}%` }}
            />
          </div>
          <span className="font-mono text-xs text-muted-foreground whitespace-nowrap">
            {totalDone}/{totalPkgs}
            {totalDone === totalPkgs && totalPkgs > 0 && (
              <span className="ml-2 text-emerald-500">✓ all done</span>
            )}
          </span>
        </div>
      </div>

      {/* Unified tree */}
      <div>
        {PHASES.map((phase, i) => (
          <PhaseRow
            key={phase.num}
            phase={phase}
            done={done}
            onToggle={toggle}
            isLast={i === PHASES.length - 1}
          />
        ))}
      </div>
    </div>
  );
}
