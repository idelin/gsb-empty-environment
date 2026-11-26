import { mergeHeaderSets, pathCovers } from "./conditions.ts";
import type { HeaderCondition, PathCondition } from "./types.ts";

const ABSENT_ATOM = "absent";
const OTHER_PRESENT_ATOM = "present:other";

interface CoverageSource {
  id: string;
  sourceRuleIds: string[];
  path: PathCondition;
  headers: readonly HeaderCondition[];
}

interface ConditionTarget {
  path: PathCondition;
  headers: readonly HeaderCondition[];
}

type HeaderCube = ReadonlyMap<string, ReadonlySet<string>>;

export interface ShadowResult {
  routeIds: string[];
  sourceRuleIds: string[];
}

function valueAtom(value: string): string {
  return `value:${JSON.stringify(value)}`;
}

function collectAtoms(
  name: string,
  conditions: readonly HeaderCondition[],
): Set<string> {
  const atoms = new Set<string>([ABSENT_ATOM, OTHER_PRESENT_ATOM]);
  for (const condition of conditions) {
    if (condition.name === name && condition.kind === "exact") {
      atoms.add(valueAtom(condition.value));
    }
  }
  return atoms;
}

function headerNames(conditions: readonly HeaderCondition[]): string[] {
  return [...new Set(conditions.map((condition) => condition.name))].sort();
}

function buildCube(
  conditions: readonly HeaderCondition[],
  allNames: readonly string[],
  allConditions: readonly HeaderCondition[],
): HeaderCube {
  const cube = new Map<string, ReadonlySet<string>>();

  for (const name of allNames) {
    const universe = collectAtoms(name, allConditions);
    const own = conditions.filter((condition) => condition.name === name);

    let allowed: Set<string> = new Set(universe);
    for (const condition of own) {
      if (condition.kind === "absent") {
        allowed = new Set([ABSENT_ATOM]);
      } else if (condition.kind === "exact") {
        allowed = new Set([valueAtom(condition.value)]);
      } else {
        allowed.delete(ABSENT_ATOM);
      }
    }
    cube.set(name, allowed);
  }

  return cube;
}

function intersectSets(
  left: ReadonlySet<string>,
  right: ReadonlySet<string>,
): Set<string> {
  const result = new Set<string>();
  for (const value of left) {
    if (right.has(value)) {
      result.add(value);
    }
  }
  return result;
}

function cubeIntersects(left: HeaderCube, right: HeaderCube): boolean {
  for (const [name, allowed] of left) {
    const other = right.get(name);
    if (other === undefined) {
      continue;
    }
    if (intersectSets(allowed, other).size === 0) {
      return false;
    }
  }
  return true;
}

/**
 * Removes cube `removed` from cube `current`, returning disjoint residual
 * cubes. The implementation works on a finite abstraction of header values:
 * absent, mentioned exact values, and one "other present value" atom.
 */
function subtractCube(current: HeaderCube, removed: HeaderCube): HeaderCube[] {
  const uncoveredAt: string[] = [];
  let fullyCovered = true;

  for (const [name, allowed] of current) {
    const removedAllowed = removed.get(name) ?? allowed;
    const common = intersectSets(allowed, removedAllowed);
    if (common.size === 0) {
      return [current];
    }
    if (common.size !== allowed.size) {
      fullyCovered = false;
    }
    if (common.size !== allowed.size) {
      uncoveredAt.push(name);
    }
  }

  if (fullyCovered) {
    return [];
  }

  const splitName = uncoveredAt[0];
  if (splitName === undefined) {
    return [current];
  }

  const currentAllowed = current.get(splitName);
  const removedAllowed = removed.get(splitName) ?? currentAllowed;
  if (currentAllowed === undefined) {
    return [current];
  }

  const pieces: HeaderCube[] = [];
  for (const atom of currentAllowed) {
    const subcube = new Map(current);
    subcube.set(splitName, new Set([atom]));
    if (removedAllowed.has(atom)) {
      pieces.push(...subtractCube(subcube, removed));
    } else {
      pieces.push(subcube);
    }
  }
  return pieces;
}

function isCoveredByCubes(
  target: HeaderCube,
  covers: readonly HeaderCube[],
): boolean {
  let remaining: HeaderCube[] = [target];
  for (const cover of covers) {
    const next: HeaderCube[] = [];
    for (const cube of remaining) {
      next.push(...subtractCube(cube, cover));
    }
    remaining = next;
    if (remaining.length === 0) {
      return true;
    }
  }
  return false;
}

function sourceCoversTarget(source: CoverageSource, target: ConditionTarget): boolean {
  if (!pathCovers(source.path, target.path)) {
    return false;
}
    return (
    mergeHeaderSets(target.headers, source.headers) !== null &&
    headerApplies(source.headers, target.headers)
  );
}

/**
 * A source header conjunction applies to every request allowed by the target
 * condition. Exact/present implications are stricter than set overlap.
 */
function headerApplies(
  sourceHeaders: readonly HeaderCondition[],
  targetHeaders: readonly HeaderCondition[],
): boolean {
  for (const source of sourceHeaders) {
    const matching = targetHeaders.filter((target) => target.name === source.name);
    if (matching.length === 0) {
      return false;
    }

    const target = matching[0];
    if (source.kind === "exact") {
      if (target.kind !== "exact" || target.value !== source.value) {
        return false;
      }
    } else if (source.kind === "absent") {
      if (target.kind !== "absent") {
        return false;
      }
    } else if (target.kind === "absent") {
      return false;
    }
  }
  return true;
}

/**
 * Finds a (usually small) inclusion-minimal set of earlier routes whose
 * conditions jointly cover the target. Header union coverage is handled by
 * subtracting cubes, so two predicates such as "present" and "absent" can
 * jointly shadow an otherwise unrestricted fallback.
 */
export function findShadowSources(
  target: ConditionTarget,
  earlierRoutes: readonly CoverageSource[],
): ShadowResult | null {
  const candidates = earlierRoutes.filter((source) => {
    if (!pathCovers(source.path, target.path)) {
      return false;
    }
    return mergeHeaderSets(target.headers, source.headers) !== null;
  });

  if (candidates.length === 0) {
    return null;
  }

  if (candidates.some((candidate) => sourceCoversTarget(candidate, target))) {
    const single = candidates.find((candidate) => sourceCoversTarget(candidate, target));
    return single === undefined
      ? null
      : { routeIds: [single.id], sourceRuleIds: [...single.sourceRuleIds] };
  }

  const names = headerNames([
    ...target.headers,
    ...candidates.flatMap((candidate) => [...candidate.headers]),
  ]);
  const allConditions = [
    ...target.headers,
    ...candidates.flatMap((candidate) => [...candidate.headers]),
  ];
  const targetCube = buildCube(target.headers, names, allConditions);
  const candidateCubes = candidates.map((candidate) =>
    buildCube(candidate.headers, names, allConditions),
  );

  if (!isCoveredByCubes(targetCube, candidateCubes)) {
    return null;
  }

  let selected = [...candidates];
  for (const candidate of [...selected]) {
    const reduced = selected.filter((item) => item.id !== candidate.id);
    const cubes = reduced.map((item) =>
      buildCube(item.headers, names, allConditions),
    );
    if (reduced.length > 0 && isCoveredByCubes(targetCube, cubes)) {
      selected = reduced;
    }
  }

  return {
    routeIds: selected.map((candidate) => candidate.id),
    sourceRuleIds: [...new Set(selected.flatMap((candidate) => candidate.sourceRuleIds))],
  };
}
