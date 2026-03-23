# Schema Audit — Decisions & Deferred TODOs

# RBAC Docs — Deferred TODOs

---

## TODO-C · PostgreSQL DB-level role grants (SEC-01)

**Blocked on:** currently single-user dev environment; no named DB roles exist yet.

**Context:**
All BTC schema tables (`009_btc.sql`) and all other schema tables have no explicit
`REVOKE FROM PUBLIC`, meaning any authenticated DB connection can SELECT from
`invoices`, `vendor_balances`, `payout_records`, `vendor_wallet_config`, etc.
This is separate from the app-layer ABAC system — DB roles protect against anything
that bypasses the app entirely (direct DB client access, compromised connection
string, reporting tools, rogue scripts).

**What needs to happen before this is implemented:**
- Decide on three DB role names: `app_role`, `readonly_role`, `audit_role`
- Create the roles in the DB (ops runbook step)
- Write a new migration (e.g. `011_grants.sql`) that:
  - `REVOKE ALL` on all BTC + core tables `FROM PUBLIC`
  - `GRANT SELECT, INSERT, UPDATE` on operational tables to `app_role`
  - `GRANT INSERT, SELECT` on `financial_audit_events` to `app_role` only
  - `GRANT SELECT` on non-sensitive tables to `readonly_role`
  - `GRANT SELECT` on `financial_audit_events` to `audit_role` only
- Update `010_btc_functions.sql` grants block: replace the conditional WARNING
  with a hard failure if `btc_app_role` does not exist
- Share all schema files (001–008) so the grants migration covers every table

**Docs to update when implemented:**
1. **`docs/security/db-access-control.md`** (new file) — document the three roles,
   what each can access, and how to provision them for a new environment.
2. **Ops runbook** — add DB role creation as a required pre-deployment step.

These items are intentionally left out of current docs because the underlying
features are not yet implemented. When each feature ships, update the docs as
described below and remove the entry from this file.

---

## TODO-A · Condition schema validation (`conditional` grants)

**Blocked on:** design doc TODO-4 — `permission_condition_templates` is never
read at runtime; condition content is not validated at grant time.

**What needs to happen in the backend before docs can be updated:**
- `GetConditionTemplate` query added to `rbac.sql`
- `AddRolePermission` service validates `conditions` against
  `required_conditions` and `forbidden_conditions` from the template
- A trigger ensures `allow_conditional = TRUE` requires a matching
  `permission_condition_templates` row

**Docs to update when implemented:**

1. **`mint/api-reference/overview.mdx`** — find the `{/* TODO-A */}` comment
   above the `conditional in V1` bullet and replace the entire bullet with
   accurate behaviour: what the validation rules look like, what error is
   returned on a schema violation, and a link to the setup guide. Remove the
   comment once done.

2. **`mint/guides/rbac/permissions-setup-guide.mdx`** — remove the `<Warning>`
   block under the conditional grant example and replace with a description of
   how the conditions schema works: what keys are valid for each permission, how
   to find the schema (via the permission's `capabilities` or a new endpoint),
   and what error code is returned when validation fails.

3. **`mint/api-reference/rbac/roles/add-role-permission.mdx`** — add a new 422
   error case (`condition_schema_violation` or similar) to the ResponseExample
   and the 422 accordion, with an explanation of how to find valid keys.

---

## TODO-B · `request` approval flow UI / interface docs

**Blocked on:** the approval request interface (the endpoints a caller uses to
check the status of a submitted request and act on a 202 response) is not yet
documented or fully implemented.

**What needs to happen before docs can be updated:**
- The request approval endpoints (`request:approve` permission) are implemented
  and documented
- The `permission_request_approvers` seeding is confirmed for
  `job_queue:configure` and `user:lock`

**Docs to update when implemented:**

1. **`mint/api-reference/overview.mdx`** — update the `request at runtime`
   legend bullet to link to the request approval endpoint docs instead of
   describing the flow inline.

2. **`mint/guides/rbac/permissions-setup-guide.mdx`** — expand the `request`
   row in the "Choosing an access type" table with a link to the approval flow
   docs. Add a section explaining how to configure approvers for a
   `request`-type permission: which roles must approve and at what level
   (`permission_request_approvers`).

3. **New guide** — add a dedicated `request-approval-guide.mdx` walking through
   the full round-trip: user hits a `request`-guarded endpoint and receives 202,
   approver finds and acts on the pending request, action finally executes.
