import type { FedCMAccount } from "./types.ts";

/** Normalizes an account identifier without silently dropping its type. */
export function normalizeAccountId(value: unknown): string | null {
  if (typeof value === "string" && value.length > 0) return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return null;
}

/** Returns the FedCM account identifier, accepting `id` and `account_id`. */
export function getAccountId(account: FedCMAccount): string | null {
  return (
    normalizeAccountId(account.id) ??
    normalizeAccountId(account.account_id)
  );
}
