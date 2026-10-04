import { CapabilityDecl, ConflictConstraint, PartyDeclaration } from './types';

/** Merged view of both parties' declarations. */
export interface MergedModel {
  parties: [string, string];
  /** Capability names offered by both parties, sorted. */
  common: string[];
  /** name -> combined priority (sum of both parties' priorities). */
  weight: Map<string, number>;
  /** source -> targets; only edges whose source is commonly supported. */
  requires: Map<string, string[]>;
  /** Normalized (a < b) mutex pairs; both endpoints commonly supported. */
  conflicts: Array<[string, string]>;
  /** Capabilities either party marked mandatory. */
  mandatory: Set<string>;
  /** All merged constraints with provenance, for conflict analysis. */
  constraints: ConflictConstraint[];
}

function indexCapabilities(
  caps: CapabilityDecl[],
): Map<string, Required<CapabilityDecl>> {
  const out = new Map<string, Required<CapabilityDecl>>();
  for (const cap of caps) {
    const existing = out.get(cap.name);
    if (!existing) {
      out.set(cap.name, {
        name: cap.name,
        priority: cap.priority === undefined ? 1 : cap.priority,
        requires: (cap.requires || []).slice(),
        conflicts: (cap.conflicts || []).slice(),
        mandatory: cap.mandatory === true,
      });
    } else {
      existing.priority = Math.max(
        existing.priority,
        cap.priority === undefined ? 1 : cap.priority,
      );
      existing.requires = Array.from(
        new Set(existing.requires.concat(cap.requires || [])),
      );
      existing.conflicts = Array.from(
        new Set(existing.conflicts.concat(cap.conflicts || [])),
      );
      existing.mandatory = existing.mandatory || cap.mandatory === true;
    }
  }
  return out;
}

/** Merge both declarations into a single constraint model. */
export function buildModel(
  declA: PartyDeclaration,
  declB: PartyDeclaration,
): MergedModel {
  const mapA = indexCapabilities(declA.capabilities);
  const mapB = indexCapabilities(declB.capabilities);
  const common = Array.from(mapA.keys())
    .filter((name) => mapB.has(name))
    .sort();

  const weight = new Map<string, number>();
  const requires = new Map<string, string[]>();
  const conflictPairs = new Map<string, [string, string]>();
  const mandatory = new Set<string>();
  const constraintIndex = new Map<string, ConflictConstraint>();

  const constraintKey = (c: {
    kind: string;
    capability: string;
    other?: string;
  }): string => `${c.kind}:${c.capability}:${c.other || ''}`;

  const addConstraint = (c: ConflictConstraint): void => {
    const key = constraintKey(c);
    const existing = constraintIndex.get(key);
    if (existing) {
      for (const p of c.declaredBy) {
        if (existing.declaredBy.indexOf(p) < 0) existing.declaredBy.push(p);
      }
    } else {
      constraintIndex.set(key, c);
    }
  };

  for (const name of common) {
    const a = mapA.get(name)!;
    const b = mapB.get(name)!;
    weight.set(name, a.priority + b.priority);

    const reqTargets = new Set<string>();
    for (const t of a.requires) reqTargets.add(t);
    for (const t of b.requires) reqTargets.add(t);
    requires.set(name, Array.from(reqTargets).sort());
    for (const t of reqTargets) {
      const declaredBy: string[] = [];
      if (a.requires.indexOf(t) >= 0) declaredBy.push(declA.party);
      if (b.requires.indexOf(t) >= 0) declaredBy.push(declB.party);
      addConstraint({
        kind: 'requires',
        capability: name,
        other: t,
        declaredBy,
      });
    }

    const confTargets = new Set<string>();
    for (const t of a.conflicts) confTargets.add(t);
    for (const t of b.conflicts) confTargets.add(t);
    for (const t of confTargets) {
      const lo = name < t ? name : t;
      const hi = name < t ? t : name;
      if (lo === hi) continue;
      const declaredBy: string[] = [];
      if (a.conflicts.indexOf(t) >= 0) declaredBy.push(declA.party);
      if (b.conflicts.indexOf(t) >= 0) declaredBy.push(declB.party);
      addConstraint({
        kind: 'conflicts',
        capability: lo,
        other: hi,
        declaredBy,
      });
      if (mapA.has(t) && mapB.has(t)) {
        conflictPairs.set(`${lo}:${hi}`, [lo, hi]);
      }
    }

    if (a.mandatory || b.mandatory) {
      mandatory.add(name);
      const declaredBy: string[] = [];
      if (a.mandatory) declaredBy.push(declA.party);
      if (b.mandatory) declaredBy.push(declB.party);
      addConstraint({ kind: 'mandatory', capability: name, declaredBy });
    }
  }

  // Mandatory capabilities that are NOT commonly supported make the
  // negotiation unsatisfiable; keep their constraints for the conflict core.
  const allMandatory = new Set(mandatory);
  for (const [party, map] of [
    [declA.party, mapA],
    [declB.party, mapB],
  ] as Array<[string, Map<string, Required<CapabilityDecl>>]>) {
    for (const cap of map.values()) {
      if (cap.mandatory && !allMandatory.has(cap.name)) {
        allMandatory.add(cap.name);
        addConstraint({
          kind: 'mandatory',
          capability: cap.name,
          declaredBy: [party],
        });
      }
    }
  }

  return {
    parties: [declA.party, declB.party].sort() as [string, string],
    common,
    weight,
    requires,
    conflicts: Array.from(conflictPairs.values()).sort((x, y) =>
      x[0] + x[1] < y[0] + y[1] ? -1 : 1,
    ),
    mandatory,
    constraints: Array.from(constraintIndex.values()),
  };
}

/**
 * Feasibility of a constraint subset against the common set.
 * The mandatory closure under `requires` must lie inside `common`
 * and contain no mutex pair.
 */
export function isFeasible(
  constraints: ConflictConstraint[],
  common: Set<string>,
): boolean {
  const mandatory = new Set<string>();
  const requires = new Map<string, string[]>();
  const pairs: Array<[string, string]> = [];
  for (const c of constraints) {
    if (c.kind === 'mandatory') mandatory.add(c.capability);
    else if (c.kind === 'requires') {
      const list = requires.get(c.capability) || [];
      list.push(c.other!);
      requires.set(c.capability, list);
    } else {
      pairs.push([c.capability, c.other!]);
    }
  }
  const closure = new Set<string>(mandatory);
  const queue = Array.from(mandatory);
  while (queue.length > 0) {
    const node = queue.pop()!;
    for (const target of requires.get(node) || []) {
      if (!closure.has(target)) {
        closure.add(target);
        queue.push(target);
      }
    }
  }
  for (const node of closure) {
    if (!common.has(node)) return false;
  }
  for (const [x, y] of pairs) {
    if (closure.has(x) && closure.has(y)) return false;
  }
  return true;
}

interface SearchState {
  selected: Set<string>;
  weight: number;
}

/**
 * Exact maximum-weight configuration via branch and bound.
 * Tie-breaks: more capabilities, then lexicographically smaller name list,
 * so the result is deterministic. Worst case is exponential in the number
 * of common capabilities; fine for realistic declaration sizes.
 */
export function solveOptimal(model: MergedModel): string[] | null {
  const common = new Set(model.common);
  if (!isFeasible(model.constraints, common)) return null;

  const closureCache = new Map<string, Set<string> | null>();
  const closureOf = (name: string): Set<string> | null => {
    const cached = closureCache.get(name);
    if (cached !== undefined) return cached;
    const closure = new Set<string>([name]);
    const queue = [name];
    let ok = true;
    while (queue.length > 0 && ok) {
      const node = queue.pop()!;
      for (const target of model.requires.get(node) || []) {
        if (!common.has(target)) {
          ok = false;
          break;
        }
        if (!closure.has(target)) {
          closure.add(target);
          queue.push(target);
        }
      }
    }
    const result = ok ? closure : null;
    closureCache.set(name, result);
    return result;
  };

  const conflictsWith = (set: Set<string>, name: string): boolean => {
    for (const [x, y] of model.conflicts) {
      if (x === name && set.has(y)) return true;
      if (y === name && set.has(x)) return true;
    }
    return false;
  };

  // Seed with the mandatory closure; infeasible seeds were rejected above.
  const seed: SearchState = { selected: new Set<string>(), weight: 0 };
  for (const name of model.mandatory) {
    const closure = closureOf(name)!;
    for (const n of closure) {
      if (!seed.selected.has(n)) {
        seed.selected.add(n);
        seed.weight += model.weight.get(n)!;
      }
    }
  }

  const candidates = model.common
    .filter((n) => !seed.selected.has(n))
    .sort((a, b) => {
      const dw = model.weight.get(b)! - model.weight.get(a)!;
      return dw !== 0 ? dw : a < b ? -1 : 1;
    });

  // Suffix sums of positive weights for the bounding function.
  const suffix = new Array<number>(candidates.length + 1).fill(0);
  for (let i = candidates.length - 1; i >= 0; i--) {
    const w = model.weight.get(candidates[i])!;
    suffix[i] = suffix[i + 1] + (w > 0 ? w : 0);
  }

  let best: SearchState | null = null;
  const betterThan = (a: SearchState, b: SearchState | null): boolean => {
    if (b === null) return true;
    if (a.weight !== b.weight) return a.weight > b.weight;
    if (a.selected.size !== b.selected.size) {
      return a.selected.size > b.selected.size;
    }
    const al = Array.from(a.selected).sort().join('');
    const bl = Array.from(b.selected).sort().join('');
    return al < bl;
  };

  const visit = (index: number, state: SearchState): void => {
    if (best !== null && state.weight + suffix[index] < best.weight) return;
    if (index >= candidates.length) {
      if (betterThan(state, best)) {
        best = {
          selected: new Set(state.selected),
          weight: state.weight,
        };
      }
      return;
    }
    const name = candidates[index];
    const closure = closureOf(name);
    if (closure !== null) {
      const next: SearchState = {
        selected: new Set(state.selected),
        weight: state.weight,
      };
      for (const n of closure) {
        if (!next.selected.has(n)) {
          next.selected.add(n);
          next.weight += model.weight.get(n)!;
        }
      }
      // Validate mutex pairs among the newly added closure members as
      // well as against the already selected ones.
      let ok = true;
      for (const n of closure) {
        if (conflictsWith(next.selected, n)) {
          ok = false;
          break;
        }
      }
      if (ok) {
        visit(index + 1, next);
      }
    }
    visit(index + 1, state);
  };

  visit(0, seed);
  if (best === null) return null;
  return Array.from((best as SearchState).selected).sort();
}
