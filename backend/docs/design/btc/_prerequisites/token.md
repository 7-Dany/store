# Prerequisites — token Extensions

> **Package:** `internal/platform/token/`
> **Files affected:** `mint.go`, `mint_test.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** Nothing — pure platform extension.
> **Blocks:** `events/ssetoken.go` — `GenerateBitcoinSSEToken` calls `token.Sign`.

---

## Overview

The existing `mint.go` provides `GenerateAccessToken` and `GenerateRefreshToken`,
both hardcoded to their specific audiences (`store:access`, `store:refresh`). The
bitcoin domain needs to issue SSE tokens with a distinct audience (`bitcoin-sse`)
and custom claims. Rather than duplicating HS256 signing boilerplate in the domain,
a single low-level `Sign` function is added.

No changes to `GenerateAccessToken`, `GenerateRefreshToken`, or any other existing
function.

---

## `token.Sign`

```go
// Sign signs any jwt.Claims implementation with HS256 using the given secret.
//
// Returns an error if:
//   - claims is nil
//   - secret is shorter than 32 bytes
//   - claims embeds jwt.RegisteredClaims and ExpiresAt is nil or zero
//     (a token with no expiry is a security hazard; all callers must set exp)
//
// This is the low-level primitive used by domain packages that need a custom
// audience, custom claims struct, or token type that does not fit the existing
// generators. Use GenerateAccessToken / GenerateRefreshToken for standard tokens.
//
// KNOWN GAP: Custom claims structs that do NOT embed jwt.RegisteredClaims bypass
// the exp check and can produce eternal tokens without error. All callers using
// non-standard claims structs MUST set an exp claim manually. This gap is accepted
// for internal use; token.Sign must NOT be exported to external packages.
func Sign(claims jwt.Claims, secret string) (string, error)
```

**Usage in `events/ssetoken.go`:**
```go
func GenerateBitcoinSSEToken(in BitcoinSSETokenInput) (string, error) {
    claims := &BitcoinSSEClaims{
        SID:     in.SID,
        IPClaim: in.IPClaim,
        RegisteredClaims: jwt.RegisteredClaims{
            Subject:   in.UserID,
            Issuer:    token.Issuer,          // "store"
            Audience:  jwt.ClaimStrings{"bitcoin-sse"},
            IssuedAt:  jwt.NewNumericDate(time.Now()),
            ExpiresAt: jwt.NewNumericDate(time.Now().Add(in.TTL)),
            ID:        in.JTI,
        },
    }
    return token.Sign(claims, in.SigningSecret)
}
```

The audience `"bitcoin-sse"` is distinct from `token.AudienceAccess` (`"store:access"`).
Callers MUST use `ParseBitcoinSSEToken` for verification — never `token.ParseAccessToken`
which validates against the wrong audience and will always reject SSE tokens.

---

## Test Inventory

**File:** `internal/platform/token/mint_test.go` (additions)

| Test | Notes |
|---|---|
| `TestSign_ValidClaims_ReturnsSigned` | |
| `TestSign_NilClaims_ReturnsError` | |
| `TestSign_ShortSecret_ReturnsError` | secret < 32 bytes → error |
| `TestSign_CanBeVerifiedWithParseClaims` | signed token verifiable via jwt.ParseWithClaims |
| `TestSign_MissingExp_ReturnsError` | zero ExpiresAt in RegisteredClaims → error |
| `TestSign_NilExp_ReturnsError` | nil ExpiresAt in RegisteredClaims → error |
| `TestSign_CustomClaimsWithoutExp_StillSigns` | custom struct without RegisteredClaims bypasses exp check; documents the known gap |
