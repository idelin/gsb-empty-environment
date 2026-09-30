// Conflict analyzer: finds inputs where allow and deny rules apply
// simultaneously. Conflict domains are normalized into pairwise-disjoint
// regions; each region gets its own minimal counterexample instance so
// distinct regions are never masked by a single representative point.

import type {
  Budget,
  Point,
  RegionReport,
  Report,
  Rule,
  Schema,
  VerificationReport,
} from './types.ts';
import {
  containsPoint,
  describeCube,
  intersectCubes,
  isCoveredBy,
  subtractCube,
  witnessOf,
  type Cube,
} from './cube.ts';
import { dnf, newDnfCache, validateCond, type DnfCache } from './dnf.ts';
import { evalCond } from './evaluate.ts';

const DEFAULT_MAX_REGIONS = 10000;
const DEFAULT_MAX_ENUM = 100000;

export class ConflictAnalyzer {
  private readonly schema: Schema;
  private readonly order: string[];
  private readonly budget: Required<Budget>;
  private readonly rules = new Map<string, Rule>();
  private readonly ruleCubes = new Map<string, Cube[]>();
  private readonly dnfCache: DnfCache = newDnfCache();
  /** Cache of allow×deny pair intersections, keyed "allowId denyId".
   *  Survives rule edits elsewhere, enabling incremental recompute. */
  private readonly pairCache = new Map<string, Cube[]>();
  private pairComputations = 0;

  constructor(schema: Schema, budget: Budget = {}) {
    for (const [attr, def] of Object.entries(schema)) {
      if (def.kind === 'enum' && def.values.length === 0)
        throw new Error(`enum attribute ${attr} has an empty domain`);
      if (def.kind === 'int' && (!Number.isInteger(def.min) || !Number.isInteger(def.max) || def.min > def.max))
        throw new Error(`integer attribute ${attr} has an invalid domain`);
    }
    this.schema = schema;
    this.order = Object.keys(schema);
    this.budget = {
      maxRegions: budget.maxRegions ?? DEFAULT_MAX_REGIONS,
      maxEnum: budget.maxEnum ?? DEFAULT_MAX_ENUM,
    };
  }

  addRule(rule: Rule): void {
    if (this.rules.has(rule.id)) throw new Error(`duplicate rule id: ${rule.id}`);
    validateCond(rule.when, this.schema);
    this.rules.set(rule.id, rule);
    this.ruleCubes.set(rule.id, dnf(rule.when, this.schema, this.order, this.dnfCache));
  }

  /** Retract a rule by id; cached pair results not involving it are kept. */
  removeRule(id: string): boolean {
    if (!this.rules.delete(id)) return false;
    this.ruleCubes.delete(id);
    for (const key of [...this.pairCache.keys()]) {
      const [allowId, denyId] = key.split(' ');
      if (allowId === id || denyId === id) this.pairCache.delete(key);
    }
    return true;
  }

  private pairKey(allowId: string, denyId: string): string {
    return `${allowId} ${denyId}`;
  }

  private conflictCubesFor(allowId: string, denyId: string): Cube[] {
    const key = this.pairKey(allowId, denyId);
    const cached = this.pairCache.get(key);
    if (cached) return cached;
    const cubes: Cube[] = [];
    for (const ca of this.ruleCubes.get(allowId)!) {
      for (const cd of this.ruleCubes.get(denyId)!) {
        const inter = intersectCubes(ca, cd);
        if (inter) cubes.push(inter);
      }
    }
    this.pairCache.set(key, cubes);
    this.pairComputations++;
    return cubes;
  }

  analyze(): Report {
    const allows = [...this.rules.values()].filter((r) => r.effect === 'allow');
    const denies = [...this.rules.values()].filter((r) => r.effect === 'deny');

    const conflictCubes: Cube[] = [];
    const minimalConflictSets: Array<{ allow: string; deny: string }> = [];
    for (const a of allows) {
      for (const d of denies) {
        const cubes = this.conflictCubesFor(a.id, d.id);
        if (cubes.length > 0) {
          minimalConflictSets.push({ allow: a.id, deny: d.id });
          conflictCubes.push(...cubes);
        }
      }
    }

    // Normalize into pairwise-disjoint regions via successive subtraction.
    let status: 'complete' | 'partial' = 'complete';
    let pendingReason: string | undefined;
    const regions: Cube[] = [];
    for (const cube of conflictCubes) {
      if (regions.length >= this.budget.maxRegions) {
        status = 'partial';
        pendingReason = `region budget ${this.budget.maxRegions} exhausted; remaining conflict candidates are undecided`;
        break;
      }
      let pieces = [cube];
      for (const region of regions) {
        pieces = pieces.flatMap((p) => subtractCube(p, region, this.schema, this.order));
      }
      regions.push(...pieces);
      if (regions.length > this.budget.maxRegions) {
        status = 'partial';
        pendingReason = `region budget ${this.budget.maxRegions} exceeded during normalization; coverage is incomplete`;
        break;
      }
    }

    const regionReports: RegionReport[] = regions.map((region, i) => {
      const allowRules = allows
        .filter((r) => isCoveredBy(region, this.ruleCubes.get(r.id)!, this.schema, this.order))
        .map((r) => r.id);
      const denyRules = denies
        .filter((r) => isCoveredBy(region, this.ruleCubes.get(r.id)!, this.schema, this.order))
        .map((r) => r.id);
      return {
        regionId: i,
        description: describeCube(region, this.schema, this.order),
        allowRules,
        denyRules,
        witness: witnessOf(region, this.schema, this.order),
      };
    });

    const rulesInvolved = [
      ...new Set(minimalConflictSets.flatMap((s) => [s.allow, s.deny])),
    ].sort();

    const verification = this.verify(regionReports, regions, status);

    return {
      status,
      ...(pendingReason ? { pendingReason } : {}),
      regions: regionReports,
      minimalConflictSets,
      rulesInvolved,
      verification,
      stats: {
        dnfCacheHits: this.dnfCache.hits,
        dnfCacheMisses: this.dnfCache.misses,
        pairComputations: this.pairComputations,
      },
    };
  }

  /** Independent enumeration cross-check over the finite domain. */
  private verify(
    regionReports: RegionReport[],
    regions: Cube[],
    searchStatus: 'complete' | 'partial',
  ): VerificationReport {
    const rules = [...this.rules.values()];
    const total = this.domainSize();
    const exhaustive = total <= this.budget.maxEnum;
    const limit = exhaustive ? total : this.budget.maxEnum;
    const mismatches: string[] = [];
    let checked = 0;
    for (const point of this.enumerate(limit)) {
      checked++;
      const hasAllow = rules.some((r) => r.effect === 'allow' && evalCond(r.when, point));
      const hasDeny = rules.some((r) => r.effect === 'deny' && evalCond(r.when, point));
      const covered = regions.some((rg) => containsPoint(rg, point));
      // An uncovered conflict point is only a mismatch when the region
      // search completed; with a partial search it belongs to the undecided
      // remainder and must not be claimed conflict-free either way.
      if (hasAllow && hasDeny && !covered && searchStatus === 'complete')
        mismatches.push(`conflict point not covered by any region: ${JSON.stringify(point)}`);
      if (!(hasAllow && hasDeny) && covered)
        mismatches.push(`region covers a non-conflict point: ${JSON.stringify(point)}`);
    }
    // Every reported region's witness must be a real allow+deny conflict.
    for (const rr of regionReports) {
      const hasAllow = rules.some((r) => r.effect === 'allow' && evalCond(r.when, rr.witness));
      const hasDeny = rules.some((r) => r.effect === 'deny' && evalCond(r.when, rr.witness));
      if (!hasAllow || !hasDeny)
        mismatches.push(`region ${rr.regionId} witness is not a conflict: ${JSON.stringify(rr.witness)}`);
    }
    return {
      mode: exhaustive ? 'exhaustive' : 'partial',
      checked,
      total,
      pending: !exhaustive,
      mismatches,
    };
  }

  private domainSize(): number {
    let total = 1;
    for (const attr of this.order) {
      const def = this.schema[attr];
      const n = def.kind === 'int' ? def.max - def.min + 1 : def.values.length;
      total *= n;
      if (total > Number.MAX_SAFE_INTEGER / 2) return Number.MAX_SAFE_INTEGER;
    }
    return total;
  }

  private *enumerate(limit: number): Generator<Point> {
    const attrs = this.order;
    const domains: Array<ReadonlyArray<number | string>> = attrs.map((attr) => {
      const def = this.schema[attr];
      if (def.kind === 'int') {
        const values: number[] = [];
        for (let v = def.min; v <= def.max; v++) values.push(v);
        return values;
      }
      return def.values;
    });
    const point: Point = {};
    let emitted = 0;
    function* rec(depth: number): Generator<Point> {
      if (emitted >= limit) return;
      if (depth === attrs.length) {
        emitted++;
        yield { ...point };
        return;
      }
      for (const v of domains[depth]) {
        point[attrs[depth]] = v;
        yield* rec(depth + 1);
        if (emitted >= limit) return;
      }
    }
    yield* rec(0);
  }

}
