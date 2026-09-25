import { runEngine, type EngineRun } from "./engine.ts";
import { ExpandError } from "./errors.ts";
import { normalizeSpec, type ExpandInput } from "./spec.ts";
import type { JsonValue } from "./json.ts";

export { ExpandError } from "./errors.ts";
export type { ErrorSource } from "./errors.ts";
export type { JsonValue } from "./json.ts";
export type {
  AxisInput,
  Condition,
  Constraint,
  ExpandInput,
} from "./spec.ts";
export type { EngineRun } from "./engine.ts";

export interface ExpandOptions {
  budget?: number;
  cursor?: string;
}

export interface ExpandResult {
  records: JsonValue[];
  emitted: number;
  complete: boolean;
  /** Exact total when complete; null while truncated (never a guess). */
  total: number | null;
  /** Resume token bound to the template/parameter digest. */
  cursor: string | null;
}

/**
 * Cheap upper bound on the record count: the raw product of domain sizes.
 * O(number of axes), no enumeration.
 */
export function estimate(input: ExpandInput): number {
  const spec = normalizeSpec(input);
  return spec.axes.reduce((acc, axis) => acc * axis.domain.length, 1);
}

/** Exact number of records the expansion emits (constraints + dedup applied). */
export function count(input: ExpandInput): number {
  const spec = normalizeSpec(input);
  return runEngine(spec, { collect: false }).emitted;
}

/** Expand up to `budget` records; resume with `cursor` after an interruption. */
export function expand(input: ExpandInput, options: ExpandOptions = {}): ExpandResult {
  const spec = normalizeSpec(input);
  const run = runEngine(spec, {
    budget: options.budget,
    cursor: options.cursor,
    collect: true,
  });
  return {
    records: run.records,
    emitted: run.emitted,
    complete: run.complete,
    total: run.total,
    cursor: run.cursor,
  };
}

export interface StreamItem {
  record: JsonValue;
  /** 0-based rank in the full deterministic enumeration. */
  rank: number;
}

export interface StreamSummary {
  complete: boolean;
  emitted: number;
  total: number | null;
  cursor: string | null;
}

/**
 * Lazy streaming generation. Records are produced one at a time; the full
 * product is never materialized. The generator's return value summarizes
 * the run and carries the resume cursor when the budget interrupted it.
 */
export function* stream(
  input: ExpandInput,
  options: ExpandOptions = {},
): Generator<StreamItem, StreamSummary, undefined> {
  const spec = normalizeSpec(input);
  const pageSize = 64;
  let cursor = options.cursor;
  let remaining = options.budget ?? Number.POSITIVE_INFINITY;

  if (remaining === 0) {
    const probe = runEngine(spec, { budget: 0, cursor, collect: false });
    return {
      complete: probe.complete,
      emitted: probe.emitted,
      total: probe.total,
      cursor: probe.cursor,
    };
  }

  while (remaining > 0) {
    const budget = Math.min(pageSize, remaining);
    const run: EngineRun = runEngine(spec, {
      budget,
      cursor,
      collect: true,
    });
    const rankBase = run.emitted - run.records.length;
    for (let i = 0; i < run.records.length; i++) {
      yield { record: run.records[i], rank: rankBase + i };
    }
    remaining -= run.records.length;
    cursor = run.cursor ?? undefined;
    if (run.complete) {
      return {
        complete: true,
        emitted: run.emitted,
        total: run.total,
        cursor: null,
      };
    }
    if (run.records.length === 0 && !run.complete) {
      // Defensive: a page made no progress; avoid an infinite loop.
      return { complete: false, emitted: run.emitted, total: null, cursor: run.cursor };
    }
  }
  // Budget exhausted exactly at a page boundary; the last page's cursor
  // resumes where we stopped.
  const tail = runEngine(spec, { budget: 0, cursor, collect: false });
  return {
    complete: tail.complete,
    emitted: tail.emitted,
    total: tail.total,
    cursor: tail.cursor,
  };
}

/**
 * Record at 0-based `rank` in the deterministic enumeration order.
 * Negative ranks count from the end (-1 = last). Returns null out of range.
 */
export function at(input: ExpandInput, rank: number): JsonValue | null {
  const spec = normalizeSpec(input);
  let target = rank;
  if (target < 0) {
    const total = runEngine(spec, { collect: false }).emitted;
    target = total + rank;
    if (target < 0) return null;
  }
  const run = runEngine(spec, { skip: target, budget: 1, collect: true });
  return run.records.length === 1 ? run.records[0] : null;
}

/** Record at reverse rank `k`: k=0 is the last record, k=1 the one before. */
export function atReverse(input: ExpandInput, k: number): JsonValue | null {
  if (k < 0) {
    throw new ExpandError("params", "$.k", "reverse rank must be >= 0");
  }
  return at(input, -(k + 1));
}
