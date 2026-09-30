/**
 * Problem model.
 *
 * Two concepts are kept strictly separate, per spec:
 * - Integer tier index: which precision tier of a component is selected
 *   (a discrete combinatorial choice).
 * - Interval error direction: each tier's error is an interval radius
 *   e >= 0 whose sign delta in [-1, +1] is adversarial at worst-case time.
 *   The tier index never enters error math; the interval direction never
 *   enters combinatorics.
 */
import { createHash } from 'node:crypto';
import { parseDecimal, cmp, ZERO } from './rational.ts';
import type { Rational } from './rational.ts';

export interface Tier {
  readonly id: string;
  readonly cost: Rational; // >= 0
  readonly error: Rational; // >= 0, interval radius
}

export interface Component {
  readonly id: string;
  readonly tiers: readonly Tier[];
  /** sourceId -> sensitivity (rational, may be negative) */
  readonly sensitivities: ReadonlyMap<string, Rational>;
}

export type SourceKind = 'shared' | 'independent';

export interface Source {
  readonly id: string;
  /**
   * shared:      one coherent error variable (e.g. calibration) drives all
   *              components referencing this source; contributions combine
   *              as a SIGNED sum before the absolute value.
   * independent: each component's error through this source is an
   *              independent interval; worst case adds |a_i| * e_i.
   */
  readonly kind: SourceKind;
}

export interface TierRef {
  readonly component: string;
  readonly tier: string;
}

export interface Exclusion {
  readonly a: TierRef;
  readonly b: TierRef;
}

export interface Dependency {
  readonly ifTier: TierRef; // selecting this tier ...
  readonly requires: TierRef; // ... forces this tier
}

export interface Instance {
  readonly name: string;
  readonly errorBudget: Rational;
  readonly sources: readonly Source[];
  readonly components: readonly Component[];
  readonly exclusions: readonly Exclusion[];
  readonly dependencies: readonly Dependency[];
}

function fail(msg: string): never {
  throw new Error(`instance: ${msg}`);
}

function parseRationalField(value: unknown, what: string): Rational {
  if (typeof value !== 'string') fail(`${what} must be a decimal string, got ${JSON.stringify(value)}`);
  try {
    return parseDecimal(value);
  } catch {
    fail(`${what} is not a valid decimal: ${JSON.stringify(value)}`);
  }
}

function requireObj(value: unknown, what: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    fail(`${what} must be an object`);
  }
  return value as Record<string, unknown>;
}

function requireString(value: unknown, what: string): string {
  if (typeof value !== 'string' || value.length === 0) fail(`${what} must be a non-empty string`);
  return value;
}

function parseTierRef(value: unknown, what: string): TierRef {
  const o = requireObj(value, what);
  return {
    component: requireString(o.component, `${what}.component`),
    tier: requireString(o.tier, `${what}.tier`),
  };
}

/** Parse and validate a JSON instance. All numbers must be decimal strings. */
export function loadInstance(raw: unknown): Instance {
  const o = requireObj(raw, 'root');
  const name = typeof o.name === 'string' ? o.name : 'unnamed';
  const errorBudget = parseRationalField(o.errorBudget, 'errorBudget');
  if (cmp(errorBudget, ZERO) < 0) fail('errorBudget must be >= 0');

  const rawSources = o.sources;
  if (!Array.isArray(rawSources) || rawSources.length === 0) fail('sources must be a non-empty array');
  const sourceIds = new Set<string>();
  const sources: Source[] = rawSources.map((rs, i) => {
    const s = requireObj(rs, `sources[${i}]`);
    const id = requireString(s.id, `sources[${i}].id`);
    if (sourceIds.has(id)) fail(`duplicate source id ${id}`);
    sourceIds.add(id);
    const kind = s.kind;
    if (kind !== 'shared' && kind !== 'independent') fail(`source ${id}: kind must be "shared" or "independent"`);
    return { id, kind };
  });

  const rawComponents = o.components;
  if (!Array.isArray(rawComponents) || rawComponents.length === 0) fail('components must be a non-empty array');
  const componentIds = new Set<string>();
  const tierIdsByComponent = new Map<string, Set<string>>();
  const components: Component[] = rawComponents.map((rc, i) => {
    const c = requireObj(rc, `components[${i}]`);
    const id = requireString(c.id, `components[${i}].id`);
    if (componentIds.has(id)) fail(`duplicate component id ${id}`);
    componentIds.add(id);
    const rawTiers = c.tiers;
    if (!Array.isArray(rawTiers) || rawTiers.length === 0) fail(`component ${id}: tiers must be a non-empty array`);
    const tierIds = new Set<string>();
    const tiers: Tier[] = rawTiers.map((rt, j) => {
      const t = requireObj(rt, `components[${i}].tiers[${j}]`);
      const tierId = requireString(t.id, `component ${id} tier[${j}].id`);
      if (tierIds.has(tierId)) fail(`component ${id}: duplicate tier id ${tierId}`);
      tierIds.add(tierId);
      const cost = parseRationalField(t.cost, `component ${id} tier ${tierId} cost`);
      const error = parseRationalField(t.error, `component ${id} tier ${tierId} error`);
      if (cmp(cost, ZERO) < 0) fail(`component ${id} tier ${tierId}: cost must be >= 0`);
      if (cmp(error, ZERO) < 0) fail(`component ${id} tier ${tierId}: error must be >= 0`);
      return { id: tierId, cost, error };
    });
    tierIdsByComponent.set(id, tierIds);
    const rawSens = requireObj(c.sensitivities, `component ${id}.sensitivities`);
    const sensitivities = new Map<string, Rational>();
    for (const [srcId, v] of Object.entries(rawSens)) {
      if (!sourceIds.has(srcId)) fail(`component ${id}: unknown source ${srcId} in sensitivities`);
      sensitivities.set(srcId, parseRationalField(v, `component ${id} sensitivity to ${srcId}`));
    }
    return { id, tiers, sensitivities };
  });

  const checkRef = (ref: TierRef, what: string) => {
    if (!componentIds.has(ref.component)) fail(`${what}: unknown component ${ref.component}`);
    if (!tierIdsByComponent.get(ref.component)!.has(ref.tier)) {
      fail(`${what}: unknown tier ${ref.tier} on component ${ref.component}`);
    }
  };

  const rawExclusions = o.exclusions ?? [];
  if (!Array.isArray(rawExclusions)) fail('exclusions must be an array');
  const exclusions: Exclusion[] = rawExclusions.map((re, i) => {
    const e = requireObj(re, `exclusions[${i}]`);
    const ex = { a: parseTierRef(e.a, `exclusions[${i}].a`), b: parseTierRef(e.b, `exclusions[${i}].b`) };
    checkRef(ex.a, `exclusions[${i}].a`);
    checkRef(ex.b, `exclusions[${i}].b`);
    return ex;
  });

  const rawDeps = o.dependencies ?? [];
  if (!Array.isArray(rawDeps)) fail('dependencies must be an array');
  const dependencies: Dependency[] = rawDeps.map((rd, i) => {
    const d = requireObj(rd, `dependencies[${i}]`);
    const dep = {
      ifTier: parseTierRef(d.if, `dependencies[${i}].if`),
      requires: parseTierRef(d.requires, `dependencies[${i}].requires`),
    };
    checkRef(dep.ifTier, `dependencies[${i}].if`);
    checkRef(dep.requires, `dependencies[${i}].requires`);
    return dep;
  });

  return { name, errorBudget, sources, components, exclusions, dependencies };
}

/** Stable JSON with sorted keys, exact rational strings only. */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  const o = value as Record<string, unknown>;
  const keys = Object.keys(o).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${canonicalize(o[k])}`).join(',')}}`;
}

/**
 * Canonical fingerprint of the whole instance, including the error budget
 * (the objective constraint). Any objective change changes this hash, which
 * is how stale certificates are detected on re-verification.
 */
export function instanceHash(inst: Instance): string {
  const view = {
    name: inst.name,
    errorBudget: rationalView(inst.errorBudget),
    sources: inst.sources.map((s) => ({ id: s.id, kind: s.kind })),
    components: inst.components.map((c) => ({
      id: c.id,
      tiers: c.tiers.map((t) => ({ id: t.id, cost: rationalView(t.cost), error: rationalView(t.error) })),
      sensitivities: Object.fromEntries(
        [...c.sensitivities.entries()].map(([k, v]) => [k, rationalView(v)]),
      ),
    })),
    exclusions: inst.exclusions,
    dependencies: inst.dependencies,
  };
  return createHash('sha256').update(canonicalize(view)).digest('hex');
}

function rationalView(r: Rational): string {
  return r.den === 1n ? r.num.toString() : `${r.num}/${r.den}`;
}
