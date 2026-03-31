# Mintlify MDX Doc Rules

This is the authoritative version. Update it here directly.

---

## Mintlify file locations

Do not rely on a hardcoded tree — read the actual directory before Stage 8:

```
list mint/api-reference/   ← find the correct domain folder and existing slug conventions
read mint/docs.json        ← find the insertion point for the new .mdx entry
```

New file path pattern: `mint/api-reference/{domain}/{route-folder}/{slug}.mdx`

Slug convention: match the existing siblings in the same domain folder (e.g. `kebab-case` of the route name).

---

## Frontmatter

```mdx
---
title: "Route Name"
description: "One sentence — what the endpoint does."
api: "METHOD http://localhost:8080/path"
---
```

---

## Request Parameters

Use `<ParamField>` for every field in the request body.

```mdx
<ParamField body="field_name" type="string" required>
  What this field is and how it's used.
</ParamField>
```

For fields with constraints, nest `<Expandable>` inside:

```mdx
<ParamField body="password" type="string" required>
  Your account password.

  <Expandable title="requirements">
    - Length: 8–72 characters
    - At least one uppercase letter
  </Expandable>
</ParamField>
```

---

## Cross-referencing Other Routes

```mdx
[`POST /me/email/request-change`](/api-reference/profile/email/request-change)
```

URL pattern: `/api-reference/{domain}/{route-folder}/{slug}`

---

## Behaviour (optional)

Add a `## Behaviour` section when the endpoint has non-obvious logic —
e.g. idempotency, silent failures, anti-enumeration design, token invalidation.

---

## Rate Limiting

Always include if the endpoint is rate limited.

```mdx
## Rate Limiting

**N requests per X minutes per IP address**.

Clients that exceed this limit will receive a `429 Too Many Requests` response
with a `Retry-After` header indicating when they may retry.
```

---

## No-Content Responses

When an endpoint returns no body (e.g. `204 No Content`), use exactly this
comment line in the `<ResponseExample>` code block — do not vary the wording:

```mdx
<ResponseExample>
```json 204
// 204 No Content — empty body
\```
</ResponseExample>
```

In the `## Responses` accordion, omit the JSON block — describe the outcome in
prose only:

```mdx
<Accordion title="204 — No Content">
  The resource was deleted successfully. No response body is returned.
</Accordion>
```

---

## No Internal Details

Write documentation from the API consumer's point of view. Describe only what
the caller can observe, rely on, or must do next.

Error messages in `<ResponseExample>` and `## Responses` must reflect the exact
JSON the API sends to the client. Never expose internal variable names, Go error
strings, database messages, or implementation-specific text.

Do not document internal storage, pipeline, or wiring details unless the client
must act on them. Avoid:

| ✗ Avoid | ✓ Use instead |
|---|---|
| `stored in btc_tx_statuses` | `saved as a tracked transaction record` |
| `kept in Redis for 30 minutes` | `expires after 30 minutes of inactivity` |
| `the events pipeline upserts rows` | `matching transactions are tracked automatically` |
| `the SQL row ID` | `the tracked transaction ID returned by this API` |
| `rate limiter runs before JWT auth` | `429 Too Many Requests` with `Retry-After` |
| `BTC_NETWORK must match` | `must match the network served by this API` |

| ✗ Avoid | ✓ Use instead |
|---|---|
| `"missing user id in context"` | `"missing or invalid access token"` |
| `"sql: no rows in result set"` | `"user not found"` |
| `"token claims cast failed"` | `"missing or invalid access token"` |
| `"context deadline exceeded"` | `"internal server error"` |

If you are unsure what message the route returns, check the handler source —
never guess or copy internal log output.

Before you mention a detail in `## Behaviour`, ask: "Does the caller need this
to use the API correctly?" If not, omit it.

---

## Callouts

Use `<Callout>` for information a developer **must not miss**. Keep `## Behaviour`
for explanation; callouts are for must-act-on alerts only. Do not duplicate
information already covered in `## Behaviour`.

### When to use

| Situation | Required? |
|---|---|
| Route requires a specific RBAC permission | **Always** |
| Playground cannot exercise the route (e.g. HttpOnly cookie auth) | **Always** |
| Destructive or irreversible action with no confirmation step | Recommended |
| Non-obvious security constraint the developer must act on | Recommended |

### Placement

Place callouts **after all `<ParamField>` blocks and before `## Behaviour`**,
unless the callout describes a playground-level limitation — in that case place
it immediately after the frontmatter (before the first `<ParamField>`).

### Permission callout — standard format

Every route guarded by a named RBAC permission must include this callout.
The format is fixed — do not rephrase:

```mdx
<Callout icon="key" color="#FFC107">
  Requires the **`permission:canonical_name`** permission.
</Callout>
```

If the route requires two permissions, list both on one line:

```mdx
<Callout icon="key" color="#FFC107">
  Requires the **`permission:one`** and **`permission:two`** permissions.
</Callout>
```

Routes that only require a valid access token (no named RBAC permission) do
**not** include this callout.

---

## RequestExample

```mdx
<RequestExample>
```json Example
{
  "field": "value"
}
\```
</RequestExample>
```

GET endpoints with no body can omit `<RequestExample>`.

DELETE and other no-body requests should include a comment indicating no body is
sent:

```mdx
<RequestExample>
```json Example
// No request body — the resource ID is passed as a path parameter.
\```
</RequestExample>
```

---

## ResponseExample

One representative response per status code. Use the status code as the tab
title (no descriptors — just the number).

```mdx
<ResponseExample>
```json 201
{ "message": "success" }
\```

```json 422
{ "code": "validation_error", "message": "field is required" }
\```
</ResponseExample>
```

Keep sidebar examples minimal — full detail lives in `## Responses`.

---

## Responses

`<AccordionGroup>` with one `<Accordion>` per status code.

```mdx
## Responses

<AccordionGroup>
  <Accordion title="201 — Created">
    Explanation of when this is returned.

    ```json
    { "message": "success" }
    ```
  </Accordion>

  <Accordion title="422 — Validation Error">
    Explanation of when this is returned.

    ```json
    { "code": "validation_error", "message": "field is required" }
    ```
  </Accordion>

  <Accordion title="429 — Rate Limited">
    The IP address has exceeded the rate limit. Check the `Retry-After` header.

    ```json
    { "code": "too_many_requests", "message": "too many requests — please slow down" }
    ```
  </Accordion>
</AccordionGroup>
```

### Accordion title format

`"STATUS_CODE — Title Case Label"`

| Status | Label |
|---|---|
| 200 | OK / Success |
| 201 | Created |
| 202 | Accepted |
| 400 | Bad Request |
| 401 | Unauthorized |
| 403 | Forbidden |
| 404 | Not Found |
| 409 | Conflict |
| 422 | Validation Error |
| 423 | Account Locked |
| 429 | Rate Limited / Too Many Attempts |
| 500 | Internal Server Error |
| 503 | Service Unavailable |

When a single status code has multiple variants (e.g. `429` for both token
lockout and IP rate limit), combine them into one accordion.

---

## docs.json — navigation tree

Every new `.mdx` file must be added to `mint/docs.json` under the correct group.
The AI must read `mint/docs.json` before producing a Stage 8 deliverable to find
the insertion point.

---

## Full template

```mdx
---
title: "Route Name"
description: "One sentence description."
api: "POST http://localhost:8080/api/v1/route"
---

<ParamField body="field" type="string" required>
  Description of the field.
</ParamField>

{/* Include the Callout below only if the route requires a named RBAC permission. */}
{/* Omit entirely for routes that only require a valid access token.             */}
<Callout icon="key" color="#FFC107">
  Requires the **`permission:canonical_name`** permission.
</Callout>

## Behaviour

Explain any non-obvious logic here.

## Rate Limiting

**N requests per X minutes per IP address**.

Clients that exceed this limit will receive a `429 Too Many Requests` response
with a `Retry-After` header indicating when they may retry.

<RequestExample>
```json Example
{
  "field": "value"
}
\```
</RequestExample>

<ResponseExample>
```json 200
{ "message": "success" }
\```
```json 422
{ "code": "validation_error", "message": "field is required" }
\```
```json 429
{ "code": "too_many_requests", "message": "too many requests — please slow down" }
\```
</ResponseExample>

## Responses

<AccordionGroup>
  <Accordion title="200 — OK">
    Explanation of when this is returned.
    ```json
    { "message": "success" }
    ```
  </Accordion>

  {/* For no-body responses, omit the JSON block entirely — prose only: */}
  {/* <Accordion title="204 — No Content">                                */}
  {/*   The resource was updated. No response body is returned.          */}
  {/* </Accordion>                                                       */}
  <Accordion title="422 — Validation Error">
    Explanation of when this is returned.
    ```json
    { "code": "validation_error", "message": "field is required" }
    ```
  </Accordion>
  <Accordion title="429 — Rate Limited">
    The IP address has exceeded the rate limit. Check the `Retry-After` header.
    ```json
    { "code": "too_many_requests", "message": "too many requests — please slow down" }
    ```
  </Accordion>
</AccordionGroup>
```
