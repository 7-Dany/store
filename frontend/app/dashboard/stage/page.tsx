"use client";
import { useState } from "react";

const BORDER = {
  Platform: "#378ADD",
  Schema: "#888780",
  Infra: "#1D9E75",
  "Stage 0": "#1D9E75",
  "Stage 2": "#BA7517",
  "Stage 2a": "#EF9F27",
  "Stage 2b": "#D85A30",
  "Stage 2c": "#E24B4A",
  Workers: "#7F77DD",
  Wiring: "#888780",
};
const TCOLOR = {
  Platform: "#185FA5",
  Schema: "#444441",
  Infra: "#085041",
  "Stage 0": "#085041",
  "Stage 2": "#633806",
  "Stage 2a": "#854F0B",
  "Stage 2b": "#712B13",
  "Stage 2c": "#791F1F",
  Workers: "#3C3489",
  Wiring: "#444441",
};

const BTC = [
  {
    num: 0,
    title: "Platform foundations",
    parallel: true,
    note: "All three are independent — start simultaneously",
    pkgs: [
      {
        name: "rpc/",
        tag: "Platform",
        desc: "Bitcoin Core HTTP client. GetRawTransaction, GetBlockHeader, GetBlock, GetBlockHash, GetBlockCount, GetBlockchainInfo. BtcToSat precision safety (math.Round, not *1e8). Startup chain match + txindex checks.",
        deps: [],
      },
      {
        name: "zmq/",
        tag: "Platform",
        desc: "ZMQ subscriber. 20-block + 20-tx worker pool. BlockEvent, TxEvent, RecoveryEvent with HashHex() byte-order reversal. safeInvoke with per-handler timeout, panic recovery, and wg drain (30s ceiling on Shutdown).",
        deps: [],
      },
      {
        name: "prerequisites/",
        tag: "Infra",
        desc: "Config vars (BTC_*), audit event constants, kvstore interface extensions (RefreshTTL, AtomicCounterStore, ListStore, PubSubStore), ConnectionCounter, RBAC PermBitcoin* constants, app.Deps new fields, token.Sign primitive.",
        deps: [],
      },
    ],
  },
  {
    num: 1,
    title: "Stage 0 domain + audit / rate / backup",
    parallel: true,
    note: "All six are parallel — each only needs one or two Phase 0 packages",
    pkgs: [
      {
        name: "txstatus/",
        tag: "Stage 0",
        desc: "Single GET /tx/:txid/status and batch GET /tx/status?ids= (up to 20). Display-only. Fresh RPC call per request, no caching, no settlement side effects.",
        deps: ["rpc/"],
      },
      {
        name: "watch/",
        tag: "Stage 0",
        desc: "In-memory sync.Map watch list per userID. SSE auth token (one-time JTI, HttpOnly cookie, sid HMAC, IPv4 /24 subnet binding). Redis ConnectionCounter for per-user SSE cap with Heartbeat every 2 min.",
        deps: ["rpc/", "zmq/"],
      },
      {
        name: "events/",
        tag: "Stage 0",
        desc: "SSE stream (display-only, best-effort). pendingMempool cap (default 10 000). spentOutpoints secondary index for RBF detection. confirmed_tx fan-out on BlockEvent. Reconnect reconciliation via txstatus.",
        deps: ["rpc/", "zmq/"],
      },
      {
        name: "audit/",
        tag: "Stage 2",
        desc: "financial_audit_events append-only table. INSERT-only DB grant. UPDATE/DELETE rejected by trigger. Write interface used by every financial package. Required fields: actor, satoshi_amounts, fiat_equivalent, invoice_id or payout_record_id.",
        deps: ["prerequisites/"],
      },
      {
        name: "rate/",
        tag: "Stage 2",
        desc: "BTC/fiat rate cache, deviation policy, stale subscription debit deferral. debit_defer_count + debit_first_deferred_at must be persisted (not in-memory) to survive restarts — Gap C fix.",
        deps: ["rpc/", "prerequisites/"],
      },
      {
        name: "wallet-backup/",
        tag: "Stage 2",
        desc: "Backup layers A/B/C/D. Recovery scenarios. Keypool cursor advance. Pre-mainnet checklist. Depends only on RPC — no other btc package depends on it.",
        deps: ["rpc/", "prerequisites/"],
      },
    ],
  },
  {
    num: 2,
    title: "Vendor",
    parallel: false,
    note: "Needs audit/ first — step-up TOTP auth writes audit events. Step-up session stored in Redis btc:stepup:{admin_id} with 15-min TTL, not in-memory (Gap E fix for multi-instance correctness).",
    pkgs: [
      {
        name: "vendor/",
        tag: "Stage 2",
        desc: "Tier config, vendor lifecycle, TOTP step-up auth (Redis session, 15-min TTL, not in-memory). Regulatory context. Tier assignment changes vendor RBAC role. invoice/ needs vendor wallet mode at invoice creation time.",
        deps: ["prerequisites/", "audit/"],
      },
    ],
  },
  {
    num: 3,
    title: "Invoice — Stage 2a",
    parallel: false,
    note: "Zero financial risk — no money moves. Ships and stabilises on testnet4 before Stage 2b starts.",
    pkgs: [
      {
        name: "invoice/",
        tag: "Stage 2a",
        desc: "Invoice creation via two-step RPC (getnewaddress + getaddressinfo ismine check). RegisterImmediate on ZMQ before returning response (Contract 1 — financial loss if skipped). invoice_address_monitoring table owned here (Contract 7). Expiry formula compensates for btc_outage_log intervals. Buyer refund address ismine check (Gap B fix).",
        deps: ["zmq/", "rpc/", "vendor/", "audit/", "prerequisites/"],
      },
    ],
  },
  {
    num: 4,
    title: "Payment + Sweep — parallel",
    parallel: true,
    note: "payment reads invoice_address_monitoring (Contract 7); sweep is independent of payment and can be built in parallel.",
    pkgs: [
      {
        name: "payment/",
        tag: "Stage 2b",
        desc: "TX detection via ZMQ subscriber. Confirmation depths. Mempool drop watchdog. Owns and writes btc_outage_log (Contract 2 — invoice reads it for expiry). On reconnect: closes outage log AND triggers resilience.HandleRecovery atomically (Contract 3).",
        deps: ["invoice/", "audit/", "prerequisites/"],
      },
      {
        name: "sweep/",
        tag: "Stage 2c",
        desc: "Full PSBT flow: walletcreatefundedpsbt → walletprocesspsbt → finalizepsbt → DB commit (constructing→broadcast + txid) → sendrawtransaction with maxfeerate guard. DB-before-network is a hard invariant. Batch up to 100 outputs. 3-block confirmation. BTC_AUTO_SWEEP_ENABLED=false default.",
        deps: ["rpc/", "audit/", "prerequisites/"],
      },
    ],
  },
  {
    num: 5,
    title: "Settlement — Stage 2b",
    parallel: false,
    note: "Gate: bitcoin_balance_drift_satoshis = 0 for ≥ 1 week on testnet4 before enabling.",
    pkgs: [
      {
        name: "settlement/",
        tag: "Stage 2b",
        desc: "Settlement phases, underpay/overpay/hybrid, atomic payout state machine (settling→settled in single TX). Calls sweep.SweepService (Contract 4 — never constructs PSBTs directly). Calls rate for subscription debits (Contract 8). Phase 1 tolerance check BEFORE settling claim.",
        deps: ["invoice/", "payment/", "rate/", "sweep/", "audit/"],
      },
    ],
  },
  {
    num: 6,
    title: "Resilience",
    parallel: false,
    note: "Stage 2c. Requires sweep to be stable on testnet4 — real Bitcoin moves after this is validated.",
    pkgs: [
      {
        name: "resilience/",
        tag: "Stage 2c",
        desc: "Degraded mode detection. Reorg rollback via settlement.rollbackSettlementFromHeight (Contract 5) — invoice + payout in same DB transaction, RowsAffected check on every UPDATE. Post-outage backfill scanning from last_processed_height cursor. HandleRecovery flow triggered by payment on reconnect.",
        deps: ["payment/", "settlement/"],
      },
    ],
  },
];

const JQ = [
  {
    num: 1,
    title: "DB + PubSub",
    parallel: true,
    note: "Fully independent of each other — start both simultaneously",
    pkgs: [
      {
        name: "006_jobqueue.sql",
        tag: "Schema",
        desc: "Creates jobs, workers, job_schedules, job_paused_kinds. Drops request_executions (replaced by kind=execute_request jobs). Strips delivery_attempts, last_attempt_at, delivery_error from request_notifications. Gate: 4 tables present, request_executions gone, 0 delivery columns.",
        deps: [],
      },
      {
        name: "kvstore Publish/Subscribe",
        tag: "Platform",
        desc: "Add Publish() and Subscribe() to RedisStore. Subscribe spawns a goroutine that reads go-redis PubSub and forwards to a chan string (closed on ctx cancel). Gate: RunPubSubContractTests green — T-C1 through T-C5.",
        deps: [],
      },
    ],
  },
  {
    num: 2,
    title: "Core types, store, metrics",
    parallel: false,
    note: "Gate: RunJobStoreContractTests green (T-26–T-32) and package builds with compile-time var _ jobqueue.PubSub = (*RedisStore)(nil) check.",
    pkgs: [
      {
        name: "job.go · store.go · metrics.go",
        tag: "Platform",
        desc: "All interfaces: Handler, HandlerFunc, Submitter, PubSub, MetricsRecorder, JobStore. pgJobStore with aging claim query (effective_priority = priority + LEAST(minutes_waited, AgingCap)). QueryMetricsRecorder (V1: SQL on each scrape) + NoopMetricsRecorder. Contract test files: store_contract_test.go, pubsub_contract_test.go, metrics_contract_test.go.",
        deps: ["006_jobqueue.sql", "kvstore Publish/Subscribe"],
      },
    ],
  },
  {
    num: 3,
    title: "Dispatcher + Worker handlers — parallel tracks",
    parallel: true,
    note: "Track B (workers) only needs Phase 2 — run alongside Track A. Merge both before Phase 4.",
    pkgs: [
      {
        name: "dispatcher · scheduler · stall · manager",
        tag: "Platform",
        desc: "N worker goroutines each with Redis SUBSCRIBE wake + 10s fallback ticker. SKIP LOCKED claim. ScheduleWatcher: single goroutine, 10s poll, cron via robfig/cron + interval. StallDetector: every 30s. Manager: Register panics if post-Start or duplicate. NewManager defaults Metrics to QueryMetricsRecorder when nil. Gate: T-01–T-25.",
        deps: ["job.go · store.go · metrics.go"],
      },
      {
        name: "worker handlers",
        tag: "Workers",
        desc: "kinds.go: add KindExecuteRequest, KindSendNotification, KindPurgeCompleted, KindPurgeExpiredPermissions. execute_request.go replaces request_executions inline execution. send_notification.go replaces delivery retry loop. purge_completed.go. Updated purge.go signature (Job not any). Gate: T-41–T-46.",
        deps: ["job.go · store.go · metrics.go"],
      },
    ],
  },
  {
    num: 4,
    title: "Admin API + WebSocket",
    parallel: false,
    note: "Gate: T-33–T-40 and T-47–T-49 green. GET /metrics must serve valid Prometheus text (content-type, label format).",
    pkgs: [
      {
        name: "api.go · ws.go",
        tag: "Platform",
        desc: "20 REST endpoints across workers, jobs, dead-letter, queues, schedules, stats, metrics. GET /metrics delegates to metrics.MetricsHandler() — zero logic in api.go. WSHub: 64-event buffered channel per client, hub goroutine never blocks on network I/O, writePump goroutine per client, drop + disconnect on full buffer. AdminRouter() fully replacing the stub.",
        deps: ["dispatcher · scheduler · stall · manager"],
      },
    ],
  },
  {
    num: 5,
    title: "Wire into server",
    parallel: false,
    note: "Gate: server boots, go test ./... green, PurgeWorker goroutine removed.",
    pkgs: [
      {
        name: "server · deps · config",
        tag: "Wiring",
        desc: "config.go: JobWorkers int, JobRetentionDays int (remove JobQueueSize). deps.go: Jobs jobqueue.Submitter, JobMgr *jobqueue.Manager. server.go: NewManager with all ManagerConfig fields, Register all four handlers, EnsureSchedule for purge_accounts_hourly + purge_completed_jobs_daily, mount /admin/jobqueue. Cleanup order: mgr.Shutdown → q.Shutdown → pool.Close.",
        deps: [
          "dispatcher · scheduler · stall · manager",
          "api.go · ws.go",
          "worker handlers",
        ],
      },
    ],
  },
];

function Tag({ label }) {
  const color = TCOLOR[label] || "#444441";
  return (
    <span
      style={{
        fontSize: "10px",
        fontWeight: 500,
        padding: "2px 7px",
        borderRadius: 100,
        border: `1px solid ${color}`,
        color,
        whiteSpace: "nowrap",
      }}
    >
      {label}
    </span>
  );
}

function Chip({ text }) {
  return (
    <span
      style={{
        fontSize: "10px",
        padding: "1px 6px",
        borderRadius: 100,
        border: "1px solid var(--color-border-secondary)",
        color: "var(--color-text-tertiary)",
        fontFamily: "var(--font-mono)",
        whiteSpace: "nowrap",
      }}
    >
      {text}
    </span>
  );
}

function Card({ pkg }) {
  const bl = BORDER[pkg.tag] || "#888780";
  return (
    <div
      style={{
        border: "1px solid var(--color-border-tertiary)",
        borderLeft: `3px solid ${bl}`,
        borderRadius: "var(--border-radius-md)",
        padding: "12px 14px",
        background: "var(--color-background-secondary)",
        flex: "1 1 210px",
        minWidth: "200px",
      }}
    >
      <div style={{ marginBottom: 6 }}>
        <Tag label={pkg.tag} />
      </div>
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 13,
          fontWeight: 500,
          color: "var(--color-text-primary)",
          marginBottom: 5,
        }}
      >
        {pkg.name}
      </div>
      <div
        style={{
          fontSize: 12,
          color: "var(--color-text-secondary)",
          lineHeight: 1.6,
          marginBottom: pkg.deps.length ? 8 : 0,
        }}
      >
        {pkg.desc}
      </div>
      {pkg.deps.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
          {pkg.deps.map((d) => (
            <Chip key={d} text={d} />
          ))}
        </div>
      )}
    </div>
  );
}

function Connector() {
  return (
    <div
      style={{
        marginLeft: 51,
        paddingTop: 8,
        paddingBottom: 8,
        display: "flex",
        flexDirection: "column",
        alignItems: "flex-start",
      }}
    >
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
        }}
      >
        <div
          style={{
            width: 1,
            height: 16,
            background: "var(--color-border-secondary)",
          }}
        />
        <svg width={10} height={6} viewBox="0 0 10 6">
          <polygon points="5,6 0,0 10,0" fill="var(--color-border-secondary)" />
        </svg>
      </div>
    </div>
  );
}

function Phase({ data }) {
  return (
    <div>
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: 12,
          marginBottom: 12,
        }}
      >
        <div
          style={{
            width: 27,
            height: 27,
            borderRadius: "50%",
            background: "var(--color-background-tertiary)",
            border: "1.5px solid var(--color-border-secondary)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 11,
            fontWeight: 500,
            flexShrink: 0,
            color: "var(--color-text-primary)",
            marginTop: 1,
          }}
        >
          {data.num}
        </div>
        <div>
          <div
            style={{
              fontSize: 14,
              fontWeight: 500,
              color: "var(--color-text-primary)",
              display: "flex",
              alignItems: "center",
              gap: 8,
              flexWrap: "wrap",
            }}
          >
            {data.title}
            {data.parallel && data.pkgs.length > 1 && (
              <span
                style={{
                  fontSize: 10,
                  padding: "2px 8px",
                  borderRadius: 100,
                  background: "var(--color-background-tertiary)",
                  color: "var(--color-text-secondary)",
                  fontWeight: 400,
                }}
              >
                parallel
              </span>
            )}
          </div>
          <div
            style={{
              fontSize: 12,
              color: "var(--color-text-secondary)",
              marginTop: 3,
              lineHeight: 1.55,
            }}
          >
            {data.note}
          </div>
        </div>
      </div>
      <div
        style={{ display: "flex", flexWrap: "wrap", gap: 10, marginLeft: 39 }}
      >
        {data.pkgs.map((p) => (
          <Card key={p.name} pkg={p} />
        ))}
      </div>
    </div>
  );
}

export default function App() {
  const [tab, setTab] = useState("btc");
  const phases = tab === "btc" ? BTC : JQ;

  return (
    <div
      style={{
        fontFamily: "var(--font-sans)",
        padding: "20px 20px 32px",
        color: "var(--color-text-primary)",
      }}
    >
      <div
        style={{
          display: "flex",
          gap: 4,
          marginBottom: 28,
          background: "var(--color-background-secondary)",
          padding: 3,
          borderRadius: "var(--border-radius-lg)",
          border: "1px solid var(--color-border-tertiary)",
          width: "fit-content",
        }}
      >
        {[
          ["btc", "bitcoin/"],
          ["jq", "jobqueue/"],
        ].map(([key, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            style={{
              padding: "6px 16px",
              borderRadius: 8,
              border: "none",
              cursor: "pointer",
              fontFamily: "var(--font-mono)",
              fontSize: 13,
              fontWeight: tab === key ? 500 : 400,
              background:
                tab === key ? "var(--color-background-primary)" : "transparent",
              color:
                tab === key
                  ? "var(--color-text-primary)"
                  : "var(--color-text-secondary)",
              boxShadow: tab === key ? "0 1px 3px rgba(0,0,0,0.08)" : "none",
              transition: "all 0.15s",
            }}
          >
            {label}
          </button>
        ))}
      </div>
      <div>
        {phases.map((phase, i) => (
          <div key={phase.num}>
            <Phase data={phase} />
            {i < phases.length - 1 && <Connector />}
          </div>
        ))}
      </div>
    </div>
  );
}
