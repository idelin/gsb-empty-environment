import { FileLayer, type TruncationResult } from "./filelayer.ts";
import type { DPTEntry, LSN, TxnTableEntry } from "./types.ts";

export interface RetentionFactor {
  name: "active-txns" | "dirty-pages" | "checkpoint";
  lsn: LSN | null;
  detail: string;
}

export interface RetentionDecision {
  ok: boolean;
  boundary: LSN | null;
  factors: RetentionFactor[];
  rationale: string[];
  refusal: string | null;
}

export interface CheckpointInfo {
  lsn: LSN;
  beginLSN: LSN;
}

/**
 * Retention boundary = min(
 *   oldest firstLSN of any active txn   (undo needs the full chain),
 *   oldest recLSN in the dirty page table (redo start),
 *   beginLSN of the last complete checkpoint (analysis anchor)
 * ).
 * Everything with lsn >= boundary must be retained.
 */
export function computeRetentionBoundary(opts: {
  activeTxns: TxnTableEntry[];
  dirtyPages: DPTEntry[];
  lastCheckpoint: CheckpointInfo | null;
  requested?: LSN;
}): RetentionDecision {
  const factors: RetentionFactor[] = [];
  const candidates: { name: string; lsn: LSN }[] = [];

  const active = opts.activeTxns.filter((t) => t.firstLSN > 0);
  const oldest = active.length ? Math.min(...active.map((t) => t.firstLSN)) : null;
  factors.push({
    name: "active-txns",
    lsn: oldest,
    detail: oldest
      ? `oldest active txn firstLSN=${oldest} — undo of a loser needs its whole chain back to here`
      : "no active transactions with log records",
  });
  if (oldest !== null) candidates.push({ name: "active-txns", lsn: oldest });

  const minRec = opts.dirtyPages.length ? Math.min(...opts.dirtyPages.map((d) => d.recLSN)) : null;
  factors.push({
    name: "dirty-pages",
    lsn: minRec,
    detail: minRec
      ? `oldest dirty-page recLSN=${minRec} — redo must start at or before this point`
      : "no dirty pages",
  });
  if (minRec !== null) candidates.push({ name: "dirty-pages", lsn: minRec });

  if (!opts.lastCheckpoint) {
    factors.push({ name: "checkpoint", lsn: null, detail: "no complete checkpoint found" });
    return {
      ok: false,
      boundary: null,
      factors,
      rationale: factors.map((f) => `[${f.name}] ${f.detail}`),
      refusal:
        "REFUSED: no complete checkpoint exists, so restart recovery cannot be anchored; " +
        "truncating now could destroy the only copy of information recovery depends on",
    };
  }
  factors.push({
    name: "checkpoint",
    lsn: opts.lastCheckpoint.beginLSN,
    detail: `last complete checkpoint beginLSN=${opts.lastCheckpoint.beginLSN} — analysis restarts from here`,
  });
  candidates.push({ name: "checkpoint", lsn: opts.lastCheckpoint.beginLSN });

  const safe = Math.min(...candidates.map((c) => c.lsn));
  const rationale = factors.map((f) => `[${f.name}] ${f.detail}`);
  rationale.push(
    `boundary = min(${candidates.map((c) => `${c.name}=${c.lsn}`).join(", ")}) = ${safe}; ` +
      `all records with lsn >= ${safe} must be retained`,
  );

  if (opts.requested !== undefined && opts.requested > safe) {
    return {
      ok: false,
      boundary: safe,
      factors,
      rationale,
      refusal: `REFUSED: requested truncation at lsn ${opts.requested} exceeds the safe boundary ${safe}`,
    };
  }
  const boundary = opts.requested !== undefined ? Math.min(opts.requested, safe) : safe;
  return { ok: true, boundary, factors, rationale, refusal: null };
}

export function executeTruncation(
  fl: FileLayer,
  decision: RetentionDecision,
): TruncationResult & { boundary: LSN } {
  if (!decision.ok || decision.boundary === null) {
    throw new Error(decision.refusal ?? "truncation refused");
  }
  const res = fl.truncateBelow(decision.boundary);
  return { ...res, boundary: decision.boundary };
}
