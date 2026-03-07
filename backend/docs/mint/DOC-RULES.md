# Mintlify MDX Doc Rules

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

- Use `required` only when the field is mandatory.
- For fields with constraints or sub-rules, nest an `<Expandable>` inside:

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

## Behaviour (optional)

Add a `## Behaviour` section when the endpoint has non-obvious logic worth explaining — e.g. idempotency, silent failures, anti-enumeration design, token invalidation rules.

---

## Rate Limiting

Always include if the endpoint is rate limited.

```mdx
## Rate Limiting

**N requests per X minutes per IP address**.

Clients that exceed this limit will receive a `429 Too Many Requests` response with a `Retry-After` header indicating when they may retry.
```

---

## RequestExample

Pin the request body in the right sidebar. One code block with `Example` as the title.

```mdx
<RequestExample>
```json Example
{
  "field": "value"
}
\```
</RequestExample>
```

- GET endpoints with no body can omit `<RequestExample>`.

---

## ResponseExample

Pin one representative response per status code in the right sidebar. Use the status code as the tab title. No descriptors — just the number.

```mdx
<ResponseExample>
```json 201
{
  "message": "success"
}
\```

```json 422
{
  "code": "validation_error",
  "message": "field is required"
}
\```
</ResponseExample>
```

- When a status code has multiple variants, pick the most common one for the sidebar.
- Keep sidebar examples minimal — full detail lives in `## Responses`.

---

## Responses

Use `<AccordionGroup>` with one `<Accordion>` per status code. Each accordion contains:
1. A sentence or two explaining **why** this response is returned.
2. A JSON code block with the response body.

```mdx
## Responses

<AccordionGroup>
  <Accordion title="201 — Created">
    Brief explanation of why this response is received.

    ```json
    {
      "message": "success"
    }
    ```
  </Accordion>

  <Accordion title="422 — Validation Error">
    Brief explanation of why this response is received.

    ```json
    {
      "code": "validation_error",
      "message": "field is required"
    }
    ```
  </Accordion>

  <Accordion title="429 — Rate Limited">
    The IP address has exceeded the rate limit. Check the `Retry-After` response header for how many seconds to wait before retrying.

    ```json
    {
      "code": "rate_limited",
      "message": "too many requests"
    }
    ```
  </Accordion>
</AccordionGroup>
```

### Accordion title format

`"STATUS_CODE — Title Case Label"`

| Status | Label |
|--------|-------|
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

### Collapsing variants under one accordion

When a single status code covers multiple scenarios (e.g. `429` for both token lockout and IP rate limit), combine them into one accordion and explain both cases in the description rather than splitting into separate accordions.

---

## Full Template

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

Clients that exceed this limit will receive a `429 Too Many Requests` response with a `Retry-After` header indicating when they may retry.

<RequestExample>
```json Example
{
  "field": "value"
}
\```
</RequestExample>

<ResponseExample>
```json 200
{
  "message": "success"
}
\```

```json 422
{
  "code": "validation_error",
  "message": "field is required"
}
\```

```json 429
{
  "code": "rate_limited",
  "message": "too many requests"
}
\```
</ResponseExample>

## Responses

<AccordionGroup>
  <Accordion title="200 — OK">
    Explanation of why this response is received.

    ```json
    {
      "message": "success"
    }
    ```
  </Accordion>

  <Accordion title="422 — Validation Error">
    Explanation of why this response is received.

    ```json
    {
      "code": "validation_error",
      "message": "field is required"
    }
    ```
  </Accordion>

  <Accordion title="429 — Rate Limited">
    The IP address has exceeded the rate limit. Check the `Retry-After` response header for how many seconds to wait before retrying.

    ```json
    {
      "code": "rate_limited",
      "message": "too many requests"
    }
    ```
  </Accordion>
</AccordionGroup>
```
