import { isViolated, propagate, type Assignment } from "./constraints.ts";
import { ExpandError } from "./errors.ts";
import type { JsonValue } from "./json.ts";
import type { NormalizedSpec } from "./spec.ts";
import { IdentityExtractor, renderTemplate } from "./template.ts";

export interface EngineOptions {
  /** Max records to emit in this run. Defaults to unbounded. */
  budget?: number;
  /** Resume token from a previous budget-interrupted run. */
  cursor?: string;
  /** Skip this many emitted records before collecting (rank positioning). */
  skip?: number;
  /** Collect records into the result. Disable for pure counting. */
  collect?: boolean;
}

export interface EngineRun {
  /** Records emitted during this run (empty when collect=false). */
  records: JsonValue[];
  /** Cumulative emitted count, including runs before a resumed cursor. */
  emitted: number;
  /** False when the budget interrupted the enumeration. */
  complete: boolean;
  /** Exact total when complete; null otherwise (never a guess). */
  total: number | null;
  /** Resume token when interrupted; null on completion. */
  cursor: string | null;
}

interface CursorPayload {
  v: 1;
  digest: string;
  /** Odometer position of the first not-yet-emitted feasible record. */
  indices: number[];
  /** Dedup identities seen so far. */
  seen: string[];
  /** Records emitted before the interruption. */
  emitted: number;
}

export function encodeCursor(payload: CursorPayload): string {
  return Buffer.from(JSON.stringify(payload), "utf8").toString("base64url");
}

function decodeCursor(cursor: string, spec: NormalizedSpec): CursorPayload {
  let payload: unknown;
  try {
    payload = JSON.parse(Buffer.from(cursor, "base64url").toString("utf8"));
  } catch {
    throw new ExpandError("cursor", "$", "cursor is not decodable");
  }
  const p = payload as Partial<CursorPayload>;
  if (
    p === null ||
    typeof p !== "object" ||
    p.v !== 1 ||
    typeof p.digest !== "string" ||
    !Array.isArray(p.indices) ||
    !Array.isArray(p.seen) ||
    typeof p.emitted !== "number"
  ) {
    throw new ExpandError("cursor", "$", "cursor payload is malformed");
  }
  if (p.digest !== spec.digest) {
    throw new ExpandError(
      "cursor",
      "$",
      "cursor is bound to a different template/parameter version; cannot resume",
    );
  }
  if (p.indices.length !== spec.axes.length) {
    throw new ExpandError("cursor", "$.indices", "cursor axis count mismatch");
  }
  p.indices.forEach((idx, d) => {
    if (
      !Number.isInteger(idx) ||
      idx < 0 ||
      idx >= spec.axes[d].domain.length
    ) {
      throw new ExpandError(
        "cursor",
        `$.indices[${d}]`,
        "cursor position is outside its axis domain",
      );
    }
  });
  return p as CursorPayload;
}

/**
 * Streaming odometer over the axes (last axis fastest) with constraint
 * propagation. Never materializes the full Cartesian product.
 */
export function runEngine(spec: NormalizedSpec, opts: EngineOptions = {}): EngineRun {
  const axes = spec.axes;
  const n = axes.length;
  let budget = opts.budget ?? Number.POSITIVE_INFINITY;
  if (budget < 0) throw new ExpandError("params", "$.budget", "budget must be >= 0");
  let skip = opts.skip ?? 0;
  const collect = opts.collect ?? true;

  let startIndices: number[] | null = null;
  let seen = new Set<string>();
  let emitted = 0;

  if (opts.cursor !== undefined) {
    const payload = decodeCursor(opts.cursor, spec);
    startIndices = payload.indices;
    seen = new Set(payload.seen);
    emitted = payload.emitted;
  }

  const indices = new Array<number>(n).fill(0);
  const assignment: Map<string, JsonValue> = new Map();
  const extractor = new IdentityExtractor(spec.identity);
  const records: JsonValue[] = [];
  let exhausted = false;
  let cursorIndices: number[] | null = null;

  function walk(depth: number, spine: boolean): void {
    if (exhausted) return;
    if (depth === n) {
      const record = renderTemplate(spec.template, assignment);
      let key: string | null = null;
      if (extractor.declared) {
        key = extractor.keyOf(record);
        if (seen.has(key)) return;
      }
      if (skip > 0) {
        skip--;
        if (key !== null) seen.add(key);
        return;
      }
      if (budget === 0) {
        exhausted = true;
        cursorIndices = indices.slice();
        return;
      }
      if (key !== null) seen.add(key);
      budget--;
      emitted++;
      if (collect) records.push(record);
      return;
    }
    const axis = axes[depth];
    const from = spine && startIndices !== null ? startIndices[depth] : 0;
    for (let v = from; v < axis.domain.length; v++) {
      indices[depth] = v;
      assignment.set(axis.name, axis.domain[v]);
      // Drop stale values of deeper axes left over from sibling subtrees,
      // so constraint checks only see the current partial assignment.
      for (let deeper = depth + 1; deeper < n; deeper++) {
        assignment.delete(axes[deeper].name);
      }
      if (spec.constraints.some((c) => isViolated(c, assignment))) continue;
      const remaining = axes.slice(depth + 1).map((a) => ({
        name: a.name,
        domain: a.domain,
      }));
      if (propagate(spec.constraints, assignment, remaining) === null) continue;
      walk(depth + 1, spine && v === from);
      if (exhausted) return;
    }
  }

  walk(0, true);

  const complete = !exhausted;
  return {
    records,
    emitted,
    complete,
    total: complete ? emitted : null,
    cursor:
      !complete && cursorIndices !== null
        ? encodeCursor({
            v: 1,
            digest: spec.digest,
            indices: cursorIndices,
            seen: [...seen],
            emitted,
          })
        : null,
  };
}
