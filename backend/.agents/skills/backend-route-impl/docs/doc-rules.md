# Mintlify MDX Doc Rules

Source of truth: `docs/mint/DOC-RULES.md`. This copy is here so Stage 8 sessions
can load it from the skill folder without navigating away.

**Always check `docs/mint/DOC-RULES.md` for the authoritative version.** If the
two files diverge, the one in `docs/mint/` wins — update this copy to match.

---

## Mintlify file locations

```
mint/
├── api-reference/
│   ├── auth/          ← auth domain endpoints
│   ├── oauth/         ← oauth domain endpoints
│   ├── profile/       ← profile domain endpoints
│   └── rbac/          ← rbac domain endpoints
├── guides/
└── docs.json          ← navigation tree — MUST be updated for every new .mdx file
```

New file path pattern: `mint/api-reference/{domain}/{route-folder}/{slug}.mdx`

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
