import { Rational } from './rational.ts';
import type { Measurement } from './measurement.ts';
import { measurement, boxOf } from './measurement.ts';
import type { LinearConstraint } from './constraint.ts';
import { judge } from './judge.ts';
import type { Judgment } from './judge.ts';
import { DEFAULT_BUDGET } from './joint.ts';
import type { Budget } from './joint.ts';

/**
 * Incremental evidence engine.
 *
 * Measurements carry a version. Judgments are cached together with the
 * versions of every measurement they depend on. Updating a measurement
 * invalidates (never silently reuses) dependent judgments: the old judgment
 * is moved to an append-only history with a supersede reason, and a fresh
 * judgment is computed on demand.
 */

interface MeasurementState {
  readonly current: Measurement;
  readonly version: number;
}

interface CachedJudgment {
  readonly serial: number;
  readonly setId: string;
  readonly judgment: Judgment;
  readonly deps: ReadonlyMap<string, number>; // measurement id -> version used
  readonly budget: Budget; // budget is part of cache validity
}

export interface HistoryRecord {
  readonly serial: number;
  readonly setId: string;
  readonly judgment: Judgment;
  readonly deps: ReadonlyMap<string, number>;
  readonly supersededBy: string; // reason, e.g. "measurement 'temp' updated to version 3"
}

export class Engine {
  private readonly measurements = new Map<string, MeasurementState>();
  private readonly constraintSets = new Map<string, readonly LinearConstraint[]>();
  private readonly cache = new Map<string, CachedJudgment>();
  private readonly historyLog: HistoryRecord[] = [];
  private serialCounter = 0;

  setMeasurement(
    id: string,
    value: Rational | string | bigint,
    error: Rational | string | bigint | 'unknown',
  ): number {
    const prev = this.measurements.get(id);
    const version = (prev?.version ?? 0) + 1;
    this.measurements.set(id, { current: measurement(id, value, error), version });
    // Incremental invalidation: archive every cached judgment that depended
    // on the OLD version of this measurement.
    for (const [setId, cached] of [...this.cache]) {
      const used = cached.deps.get(id);
      if (used !== undefined && used !== version) {
        this.historyLog.push({
          serial: cached.serial,
          setId,
          judgment: cached.judgment,
          deps: cached.deps,
          supersededBy: `measurement '${id}' updated to version ${version}`,
        });
        this.cache.delete(setId);
      }
    }
    return version;
  }

  getMeasurement(id: string): Measurement {
    const s = this.measurements.get(id);
    if (s === undefined) throw new Error(`unknown measurement '${id}'`);
    return s.current;
  }

  versionOf(id: string): number {
    const s = this.measurements.get(id);
    if (s === undefined) throw new Error(`unknown measurement '${id}'`);
    return s.version;
  }

  registerConstraintSet(setId: string, constraints: readonly LinearConstraint[]): void {
    const existing = this.cache.get(setId);
    if (existing !== undefined) {
      this.historyLog.push({
        serial: existing.serial,
        setId,
        judgment: existing.judgment,
        deps: existing.deps,
        supersededBy: `constraint set '${setId}' re-registered`,
      });
      this.cache.delete(setId);
    }
    this.constraintSets.set(setId, constraints);
  }

  /** Returns the cached judgment if all dependency versions still match. */
  evaluate(setId: string, budget: Budget = DEFAULT_BUDGET): Judgment {
    const constraints = this.constraintSets.get(setId);
    if (constraints === undefined) throw new Error(`unknown constraint set '${setId}'`);

    const varIds = [...new Set(constraints.flatMap((c) => [...c.terms.keys()]))];
    for (const v of varIds) {
      if (!this.measurements.has(v)) throw new Error(`constraint set '${setId}' references unknown measurement '${v}'`);
    }
    const deps = new Map<string, number>(varIds.map((v) => [v, this.measurements.get(v)!.version]));

    const cached = this.cache.get(setId);
    if (
      cached !== undefined &&
      sameDeps(cached.deps, deps) &&
      cached.budget.maxCombinations === budget.maxCombinations
    ) {
      return cached.judgment;
    }

    const measurements = varIds.map((v) => this.measurements.get(v)!.current);
    const judgment = judge(constraints, varIds, boxOf(measurements), budget);
    this.cache.set(setId, { serial: ++this.serialCounter, setId, judgment, deps, budget });
    return judgment;
  }

  history(): readonly HistoryRecord[] {
    return this.historyLog;
  }
}

function sameDeps(a: ReadonlyMap<string, number>, b: ReadonlyMap<string, number>): boolean {
  if (a.size !== b.size) return false;
  for (const [k, v] of a) if (b.get(k) !== v) return false;
  return true;
}
