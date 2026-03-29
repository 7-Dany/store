# Frontend Manual Testing Checklist — Store

> **How to use:** Work through each section top-to-bottom. Tick items as you go.
> Items marked ⚠️ require a specific backend state (e.g. locked account) to trigger.
> Items marked 🔗 require an OAuth provider to be configured.

---

## 0. Setup

- [ ] Backend is running at `http://localhost:8080`
- [ ] Frontend is running (dev or build)
- [ ] A real email inbox is accessible (for OTP codes)
- [ ] Browser DevTools → Network tab open to inspect API calls & cookies
- [ ] Light **and** dark mode tested (use the theme toggle in the top-right corner of auth pages)

---

## 1. Root Redirect

| # | Action | Expected |
|---|--------|----------|
| 1.1 | Navigate to `/` | Immediately redirects to `/login` with no flash |

---

## 2. Login Page (`/login`)

### 2.1 Page Render

| # | Action | Expected |
|---|--------|----------|
| 2.1.1 | Open `/login` | Store logo, "Sign in to Store" heading, step-dot indicator at step 0 visible |
| 2.1.2 | Check query param `?verified=1&email=alice@example.com` | Green "✓ Email verified!" banner shows; identifier field pre-filled with `alice@example.com` and form is on the password step |
| 2.1.3 | Check query param `?reset=1` | Green "✓ Password reset! Sign in with your new password." banner shows |
| 2.1.4 | Check query param `?email=alice%40example.com` (no `verified`) | Identifier field pre-filled; form starts on the password step (step 1) |

### 2.2 Identifier Step

| # | Action | Expected |
|---|--------|----------|
| 2.2.1 | Submit empty identifier | "Please enter your email or username." inline error; no API call made |
| 2.2.2 | Submit identifier longer than 254 chars | "Identifier is too long." inline error |
| 2.2.3 | Enter a valid identifier, click Continue | Slides forward to the password step; identifier is displayed above the password field; step-dot advances to index 1 |
| 2.2.4 | Identifier field auto-focuses | Input has focus on load without user interaction |

### 2.3 Password Step

| # | Action | Expected |
|---|--------|----------|
| 2.3.1 | Click ← Back | Slides back to the identifier step; password field is cleared |
| 2.3.2 | Submit empty password | "Please enter your password." inline error |
| 2.3.3 | Click the eye icon | Password toggles between `type="password"` and `type="text"` |
| 2.3.4 | Type wrong password and submit | API `POST /auth/login` called; error message "Incorrect email/username or password." appears on the password field |
| 2.3.5 | Clear the password field after an error | Error message disappears immediately on change |
| 2.3.6 | Submit correct credentials | Redirects to `/dashboard`; refresh-token HttpOnly cookie set (check DevTools → Application → Cookies) |

### 2.4 Edge-Case Login Responses

| # | Action | Expected |
|---|--------|----------|
| 2.4.1 | Login with correct password but **unverified email** | Redirects to `/verify-email?email=<encoded>`. `__pending_login__` key present in `sessionStorage`. A new verification code was dispatched (backend fires `resend-verification` automatically). |
| 2.4.2 ⚠️ | Login with an **admin-locked account** (423) | Error on password field: "Your account is locked. Please contact support." |
| 2.4.3 ⚠️ | Login after 10 consecutive wrong passwords (429 `login_locked`) | Error: "Too many failed attempts. Please wait before trying again." |
| 2.4.4 ⚠️ | Login with a **suspended account** (403 `account_inactive`) | Error: "Your account has been suspended. Contact support." |

### 2.5 Forgot Password Link

| # | Action | Expected |
|---|--------|----------|
| 2.5.1 | Reach the password step with `alice@example.com`, click "Forgot password?" | Navigates to `/forgot-password?email=alice%40example.com`; forgot-password form pre-fills email field |

### 2.6 OAuth Buttons

| # | Action | Expected |
|---|--------|----------|
| 2.6.1 🔗 | Click "Continue with Google" | Browser navigates to backend Google OAuth initiation URL (`/api/v1/oauth/google`) |
| 2.6.2 🔗 | Click "Continue with Telegram" (with `NEXT_PUBLIC_TELEGRAM_BOT_USERNAME` set) | Telegram auth popup opens |
| 2.6.3 | `NEXT_PUBLIC_TELEGRAM_BOT_USERNAME` **not** set | Amber warning banner: "Set `NEXT_PUBLIC_TELEGRAM_BOT_USERNAME` in .env.local" |

### 2.7 Footer Links

| # | Action | Expected |
|---|--------|----------|
| 2.7.1 | Click "Sign up" | Navigates to `/register` |

---

## 3. Register Page (`/register`)

### 3.1 Page Render

| # | Action | Expected |
|---|--------|----------|
| 3.1.1 | Open `/register` | Logo, "Create your account" heading, 4 step-dots at step 0 |

### 3.2 Step 0 — Name

| # | Action | Expected |
|---|--------|----------|
| 3.2.1 | Submit with empty name | "Please enter your name." error |
| 3.2.2 | Submit with a name over 100 chars | "Name must be under 100 characters." error |
| 3.2.3 | Submit a valid name | Slides forward to email step; step-dot advances |

### 3.3 Step 1 — Email

| # | Action | Expected |
|---|--------|----------|
| 3.3.1 | Click ← Back | Returns to name step |
| 3.3.2 | Submit empty email | "Please enter your email." error |
| 3.3.3 | Submit invalid email format | "Please enter a valid email address." error |
| 3.3.4 | Submit valid email | Slides forward to password step; heading personalises with first name (e.g. "Nice to meet you, Alice!") |

### 3.4 Step 2 — Password

| # | Action | Expected |
|---|--------|----------|
| 3.4.1 | Click ← Back | Returns to email step |
| 3.4.2 | Type a weak password | Password-strength indicator reflects weakness (e.g. missing uppercase/symbol) |
| 3.4.3 | Submit a password failing any of the 4 rules | Inline error listing the first unmet rule |
| 3.4.4 | Type a fully valid password (8+ chars, upper, lower, digit, symbol) | Strength indicator is fully satisfied |
| 3.4.5 | Toggle the eye icon | Password visibility toggles |
| 3.4.6 | Submit valid password | `POST /auth/register` called; on success slides to OTP step; `__pending_login__` written to `sessionStorage` |
| 3.4.7 ⚠️ | Submit with an already-registered email | Form navigates back to the email step (step 1) with error "An account with that email already exists." |

### 3.5 Step 3 — Email Verification OTP

| # | Action | Expected |
|---|--------|----------|
| 3.5.1 | Click ← Back | Returns to password step |
| 3.5.2 | Submit fewer than 6 digits | "Enter all 6 digits of the code." error |
| 3.5.3 | "Resend code" button | Visible immediately (cooldown starts when step loads); after 120 s becomes clickable |
| 3.5.4 | Click "Resend code" | Spinner appears; button disabled during resend; 120 s cooldown restarts; OTP field cleared |
| 3.5.5 | Submit a wrong 6-digit code | Error: "Incorrect or expired code. Request a new one below." |
| 3.5.6 | Submit the correct code | `POST /auth/verify-email` called → `POST /auth/login` auto-called with `sessionStorage` credentials → `__pending_login__` removed → redirects to `/dashboard` |
| 3.5.7 ⚠️ | Correct code but auto-login fails | Falls back to `/login?verified=1&email=<email>` |

---

## 4. Verify-Email Page (`/verify-email`)

> Accessed from: (a) login 403 `email_not_verified` redirect, or (b) direct navigation.

| # | Action | Expected |
|---|--------|----------|
| 4.1 | Open `/verify-email?email=alice%40example.com` | Email `alice@example.com` shown in the description text; 120 s resend cooldown starts immediately (code was already sent by the login hook) |
| 4.2 | Submit wrong code | Error shown on OTP field |
| 4.3 | Submit correct code with `__pending_login__` in `sessionStorage` | Auto-logins → redirects to `/dashboard`; `sessionStorage` entry removed |
| 4.4 | Submit correct code **without** `__pending_login__` | Redirects to `/login?verified=1&email=<email>` |
| 4.5 | "Back to Sign in" link | Navigates to `/login` |

---

## 5. Forgot-Password Page (`/forgot-password`)

### 5.1 Step 0 — Email

| # | Action | Expected |
|---|--------|----------|
| 5.1.1 | Open `/forgot-password?email=alice%40example.com` | Email field pre-filled |
| 5.1.2 | Submit empty email | "Please enter your email address." error |
| 5.1.3 | Submit invalid email format | "Please enter a valid email address." error |
| 5.1.4 | Submit valid email (even non-existent) | `POST /auth/forgot-password` always returns 202; form always advances to OTP step regardless of whether account exists (anti-enumeration) |

### 5.2 Step 1 — OTP

| # | Action | Expected |
|---|--------|----------|
| 5.2.1 | Click ← Back | Returns to email step |
| 5.2.2 | 60 s resend cooldown starts | "Resend in X:XX" shown; button disabled during countdown |
| 5.2.3 | Click "Resend code" after countdown | `POST /auth/forgot-password` re-fired; cooldown resets; OTP field cleared |
| 5.2.4 | Submit wrong code | Error: "Incorrect or expired code. Check it and try again." |
| 5.2.5 | Submit expired code (410) | Error: "This reset code has expired. Please request a new one." |
| 5.2.6 ⚠️ | Too many wrong attempts (429) | Error: "Too many incorrect attempts. Please request a new reset code." |
| 5.2.7 | Submit correct code | `POST /auth/verify-reset-code` returns a `reset_token`; advances to new-password step |

### 5.3 Step 2 — New Password

| # | Action | Expected |
|---|--------|----------|
| 5.3.1 | Password strength indicator present and responsive | Reacts to typing as in the register flow |
| 5.3.2 | Confirm-password field shows live match feedback | Green "Passwords match" / muted "Passwords don't match" as user types |
| 5.3.3 | Submit with mismatched passwords | "Passwords don't match." error on confirm field |
| 5.3.4 | Submit a password that doesn't meet strength rules | Inline error for the first failed rule |
| 5.3.5 | Submit valid matching passwords | `POST /auth/reset-password` called with the `reset_token`; redirects to `/login?reset=1` |
| 5.3.6 | Verify all sessions are cleared after reset | Re-logging in from another browser/tab is required (old refresh cookie rejected) |

---

## 6. Dashboard (`/dashboard`)

### 6.1 Access Control

| # | Action | Expected |
|---|--------|----------|
| 6.1.1 | Navigate to `/dashboard` without being logged in | Redirected to `/login` (Next.js middleware or layout check) |
| 6.1.2 | Navigate to `/dashboard` while authenticated | Page loads; sidebar and overview content visible |

### 6.2 OAuth Provider Banners

| # | Action | Expected |
|---|--------|----------|
| 6.2.1 🔗 | Arrive at `/dashboard?provider=google&action=linked` | Subtitle: "Google account linked successfully." |
| 6.2.2 🔗 | Arrive at `/dashboard?provider=google` (no `action`) | Subtitle: "Signed in with Google." |
| 6.2.3 | No query params | Default subtitle: "Here's what's happening in your store today." |

### 6.3 Sidebar

| # | Action | Expected |
|---|--------|----------|
| 6.3.1 | Sidebar shows "Store / Admin" header | Logo and labels rendered |
| 6.3.2 | Navigate through each nav item (Overview, Orders, Products, Customers, Analytics, Settings) | Active item highlighted; URL changes accordingly |
| 6.3.3 | Click the sidebar toggle (hamburger) | Sidebar collapses to icon-only mode; state persists via `sidebar_state` cookie on reload |
| 6.3.4 | On mobile viewport | Sidebar uses sheet/drawer behavior |
| 6.3.5 | User info in footer | Displays `display_name` and `#username` (or email if no username); avatar initials correct |

### 6.4 User Menu (Dropdown)

| # | Action | Expected |
|---|--------|----------|
| 6.4.1 | Click user info in sidebar footer | Dropdown opens with user details, Profile, Settings, Theme submenu, Sign out |
| 6.4.2 | Open Theme submenu → click each option | Theme changes immediately; active option shows checkmark |
| 6.4.3 | Click "Profile" | Navigates to `/dashboard/profile` |
| 6.4.4 | Click "Settings" | Navigates to `/dashboard/settings` |
| 6.4.5 | Click "Sign out" | "Signing out…" spinner shows; `POST /api/auth/logout` called; `refresh_token` cookie cleared; redirects to `/login` |

### 6.5 Overview Page Content

| # | Action | Expected |
|---|--------|----------|
| 6.5.1 | Stats cards visible | 4 cards: Total Revenue, Orders, Customers, Products |
| 6.5.2 | Recent Orders table | 6 mock rows; status badges colour-coded correctly (Delivered=green, Processing=primary, Shipped=blue, Cancelled=red) |
| 6.5.3 | Top Products panel | 5 items with progress bars; low-stock items (< 20) show warning in red with ⚠ |

---

## 7. Session & Token Behaviour

| # | Action | Expected |
|---|--------|----------|
| 7.1 | After login, inspect DevTools → Application → Cookies | `refresh_token` HttpOnly cookie present, scoped appropriately |
| 7.2 | Access token expires in 15 min; page still works | Frontend auto-refreshes via `POST /auth/refresh` using the cookie transparently |
| 7.3 | Logout, then click browser Back to `/dashboard` | Redirected to `/login` (no stale session) |
| 7.4 ⚠️ | Replay an old refresh token after logout | Backend returns 401 `invalid_token`; frontend should redirect to login |

---

## 8. Cross-Cutting Concerns

| # | Check | Expected |
|---|-------|----------|
| 8.1 | **Network error** (stop backend, try to login) | "Could not reach the server. Check your connection." displayed |
| 8.2 | **Loading states** | Every async action shows a spinner/loading label on the submit button; button disabled during request |
| 8.3 | **Theme toggle** (top-right on auth pages) | Switches between light and dark; no flash on load |
| 8.4 | **Resend cooldown persistence** | Cooldown timer is accurate and does not reset on hot reload / component re-render |
| 8.5 | **Step-dot indicators** | Dots accurately reflect the current step on login (2 dots), register (4 dots), forgot-password (3 dots) |
| 8.6 | **Slide animations** | Forward transitions slide from the right; back transitions slide from the left |
| 8.7 | **Responsive layout** | All auth pages remain usable at 375 px viewport width |
| 8.8 | **sessionStorage cleanup** | After a successful verify-email auto-login, `__pending_login__` is gone from sessionStorage |

---

## 9. Backend Endpoint Coverage Summary

The table below maps each backend endpoint to the frontend flow that exercises it. Use it to confirm every endpoint is reachable from the UI.

| Backend Endpoint | Triggered By |
|-----------------|--------------|
| `POST /auth/login` | Login form step 2 submit; auto-login after OTP verify |
| `POST /auth/register` | Register form step 2 (password) submit |
| `POST /auth/verify-email` | Register OTP step; standalone Verify-Email page |
| `POST /auth/resend-verification` | "Resend code" on OTP step; fired automatically on unverified-login redirect |
| `POST /auth/forgot-password` | Forgot-password step 0 email submit; also fires on "Resend code" in step 1 |
| `POST /auth/verify-reset-code` | Forgot-password step 1 OTP submit |
| `POST /auth/reset-password` | Forgot-password step 2 new-password submit |
| `POST /auth/refresh` | Transparent token refresh by the API client when access token is near expiry |
| `POST /auth/logout` | "Sign out" in sidebar user menu |
| `GET /oauth/google` | "Continue with Google" button click |
| `GET /oauth/google/callback` | Browser callback from Google redirect |
| `POST /api/oauth/telegram` (Next.js route) | Telegram widget `onTelegramAuth` callback |
| `GET /profile/me` | Dashboard layout SSR (fetches user for sidebar) |

---

*Last updated: based on frontend `components/auth/`, `hooks/auth/`, `app/(auth)/`, `app/dashboard/` and backend `internal/domain/auth/` source.*
