import { FedCMValidationError } from "./errors.ts";
import type { FedCMAccount } from "./types.ts";
import { getAccountId } from "./util.ts";

function isEmailClaim(name: string): boolean {
  return name.toLowerCase().includes("email");
}

/**
 * Only the literal boolean `true` (or the form-encoded string "true") counts
 * as an affirmative disclosure. A missing value, `false`, `"false"`, an empty
 * string or any other value withholds email.
 */
export function normalizeDisclosureTextShown(value: unknown): boolean {
  if (value === true) return true;
  return typeof value === "string" && value.trim().toLowerCase() === "true";
}

export function disclosureAllowsEmail(value: unknown): boolean {
  return normalizeDisclosureTextShown(value);
}

export function assertionContainsEmail(
  claims: Readonly<Record<string, unknown>>,
): boolean {
  return Object.keys(claims).some(isEmailClaim);
}

function removeEmailClaims(claims: Record<string, unknown>): void {
  for (const key of Object.keys(claims)) {
    if (isEmailClaim(key)) delete claims[key];
  }
}

/**
 * Removes email-related claims when the disclosure text was not shown. Other
 * claims are preserved so a previously built assertion can be passed through
 * safely.
 */
export function sanitizeAssertionClaims(
  claims: Readonly<Record<string, unknown>>,
  disclosureTextShown: unknown,
): Record<string, unknown> {
  const result: Record<string, unknown> = { ...claims };
  if (!normalizeDisclosureTextShown(disclosureTextShown)) {
    removeEmailClaims(result);
  }
  return result;
}

/**
 * Throws if a caller would otherwise sign an assertion containing email while
 * the browser did not report that the disclosure text was shown.
 */
export function assertNoUndisclosedEmail(
  claims: Readonly<Record<string, unknown>>,
  disclosureTextShown: unknown,
): void {
  if (
    !normalizeDisclosureTextShown(disclosureTextShown) &&
    assertionContainsEmail(claims)
  ) {
    throw new FedCMValidationError(
      "disclosure_not_shown",
      "email claims cannot be issued when disclosure_text_shown is not true",
    );
  }
}

export interface BuildIdAssertionClaimsOptions {
  account: FedCMAccount;
  /** Value from the id_assertion request body. */
  disclosureTextShown?: unknown;
  /** Claims supplied by the identity provider. Email is filtered when needed. */
  claims?: Readonly<Record<string, unknown>>;
  /**
   * Merge safe account fields such as `name` and `picture` into the
   * assertion. Defaults to `true`; email is still gated by disclosure.
   */
  includeAccountClaims?: boolean;
}

/**
 * Builds the claims to sign for an id_assertion response. When
 * `disclosure_text_shown` is missing or false, any `email`/`email_verified`
 * claims are removed, including ones supplied by the caller.
 */
export function buildIdAssertionClaims(
  options: BuildIdAssertionClaimsOptions,
): Record<string, unknown> {
  const accountId = getAccountId(options.account);
  if (!accountId) {
    throw new FedCMValidationError(
      "invalid_request",
      "Cannot build an assertion for an account without an id",
      500,
    );
  }

  const allowed = normalizeDisclosureTextShown(options.disclosureTextShown);
  const result: Record<string, unknown> = { ...(options.claims ?? {}) };

  if (options.includeAccountClaims !== false) {
    for (const [key, value] of Object.entries(options.account)) {
      if (key === "id" || key === "account_id" || key.startsWith("_")) {
        continue;
      }
      if (!allowed && isEmailClaim(key)) continue;
      result[key] = value;
    }
  }

  result.sub = accountId;
  if (!allowed) removeEmailClaims(result);
  return result;
}
