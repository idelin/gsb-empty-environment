/**
 * Global registry of verified summaries and poison markers.
 *
 * Runtime mutual recursion: the verifier (`verifier.ts`) looks up callees
 * here and poisons failed ids, while this module imports the verifier to
 * trigger on-demand verification through {@link ensureVerified}. ESM live
 * bindings make the cycle safe.
 */
import type { VerifiedSummary } from "./summary.ts";
import { verifyImplication } from "./verifier.ts";
import type { Contract, Implementation } from "./model.ts";

interface PoisonEntry {
  readonly status: "poison";
  readonly reason: string;
}

interface VerifiedEntry {
  readonly status: "verified";
  readonly summary: VerifiedSummary;
}

interface PendingEntry {
  readonly status: "pending";
}

type Entry = PoisonEntry | VerifiedEntry | PendingEntry;

const entries = new Map<string, Entry>();

export function lookupSummary(id: string): VerifiedSummary | null {
  const entry = entries.get(id);
  return entry && entry.status === "verified" ? entry.summary : null;
}

/** Poison ids whose verification failed; their annotation must not propagate. */
export function markPoison(id: string, reason: string): void {
  entries.set(id, { status: "poison", reason });
}

export function markPending(id: string): void {
  entries.set(id, { status: "pending" });
}

export function registerSummary(summary: VerifiedSummary): void {
  entries.set(summary.id, { status: "verified", summary });
}

export function isPoisoned(id: string): boolean {
  const entry = entries.get(id);
  return entry?.status === "poison";
}

export function isPending(id: string): boolean {
  const entry = entries.get(id);
  return entry?.status === "pending";
}

export function registryStatus(id: string): "unknown" | "pending" | "verified" | "poison" {
  const entry = entries.get(id);
  return entry ? entry.status : "unknown";
}

/**
 * On-demand verification used by call edges and mutually recursive contracts.
 * A pending id encountered again means the dependency graph has a cycle; the
 * current finite model cannot be compositionally verified, so it is poisoned.
 */
export function ensureVerified(
  contract: Contract,
  implementation: Implementation,
): VerifiedSummary | null {
  const existing = lookupSummary(contract.id);
  if (existing) {
    return existing;
  }
  if (isPoisoned(contract.id) || isPending(contract.id)) {
    return null;
  }
  const result = verifyImplication(contract, implementation);
  return result.ok ? result.summary : null;
}

/** Test/setup helper: clear every registered summary and marker. */
export function resetRegistry(): void {
  entries.clear();
}
