// ─── Types ───────────────────────────────────────────────────────────────────

export interface PasswordChecks {
  length: boolean;
  upper: boolean;
  lower: boolean;
  digit: boolean;
  symbol: boolean;
}

export type StrengthLevel = "empty" | "weak" | "fair" | "good" | "strong";

export interface PasswordStrength {
  checks: PasswordChecks;
  score: number; // 0–5
  level: StrengthLevel;
  label: string;
}

// ─── Constants ───────────────────────────────────────────────────────────────

export const PASSWORD_REQUIREMENTS: {
  key: keyof PasswordChecks;
  label: string;
}[] = [
  { key: "length", label: "8+ characters" },
  { key: "upper", label: "Uppercase letter" },
  { key: "lower", label: "Lowercase letter" },
  { key: "digit", label: "Number" },
  { key: "symbol", label: "Symbol" },
];

const SCORE_TO_LEVEL: StrengthLevel[] = [
  "empty",
  "weak",
  "fair",
  "good",
  "strong",
  "strong",
];
const SCORE_TO_LABEL = ["", "Weak", "Fair", "Good", "Strong", "Very strong"];

// ─── Analyser ────────────────────────────────────────────────────────────────

export function analyzePassword(value: string): PasswordStrength {
  if (!value) {
    return {
      checks: {
        length: false,
        upper: false,
        lower: false,
        digit: false,
        symbol: false,
      },
      score: 0,
      level: "empty",
      label: "",
    };
  }

  const checks: PasswordChecks = {
    length: value.length >= 8,
    upper: /[A-Z]/.test(value),
    lower: /[a-z]/.test(value),
    digit: /[0-9]/.test(value),
    symbol: /[^A-Za-z0-9]/.test(value),
  };

  const score = Object.values(checks).filter(Boolean).length;

  return {
    checks,
    score,
    level: SCORE_TO_LEVEL[score],
    label: SCORE_TO_LABEL[score],
  };
}
