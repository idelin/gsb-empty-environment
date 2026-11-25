import type { SerializedDfa } from "./dfa.ts";
import {
  deserializeDfa,
  dfaDigest,
  dfaUnionAll,
  serializeDfa,
} from "./dfa.ts";
import type { ChainEvidence } from "./chain.ts";
import { compareStringArrays, uniqueSorted } from "./util.ts";

export interface PathConflict {
  left: string;
  right: string;
  reasons: string[];
}

export interface IndependentSelection {
  paths: ChainEvidence[];
  weight: number;
  overflow: boolean;
}

export interface CompatibleSet {
  pathIds: string[];
  paths: ChainEvidence[];
  weight: number;
  language: SerializedDfa;
  languageDigest: string;
  intermediateDomains: string[];
  revocationPredecessors: string[];
  maximal: boolean;
}

export interface CompatibleSetEnumeration {
  sets: CompatibleSet[];
  truncated: boolean;
}

export interface CompatibleSetOptions {
  maximalOnly?: boolean;
  limit?: number;
  includeInvalid?: boolean;
}

const MAX_EXACT_CANDIDATES = 60;

export function pathConflictReasons(left: ChainEvidence, right: ChainEvidence): string[] {
  const reasons: string[] = [];
  if (left.pathId === right.pathId) reasons.push("duplicate-path");

  const leftEdges = new Set(left.edgeIds);
  for (const edge of right.edgeIds) {
    if (leftEdges.has(edge)) {
      reasons.push(`shared-edge:${edge}`);
      break;
    }
  }

  const leftDomains = new Set(left.intermediateDomains);
  for (const domain of right.intermediateDomains) {
    if (leftDomains.has(domain)) reasons.push(`shared-admin-domain:${domain}`);
  }

  const leftPredecessors = new Set(left.revocationPredecessors);
  for (const predecessor of right.revocationPredecessors) {
    if (leftPredecessors.has(predecessor)) reasons.push(`shared-revocation-predecessor:${predecessor}`);
  }
  return [...new Set(reasons)];
}

export function pathsConflict(left: ChainEvidence, right: ChainEvidence): boolean {
  return pathConflictReasons(left, right).length > 0;
}

export function conflictsFor(paths: ChainEvidence[]): PathConflict[] {
  const conflicts: PathConflict[] = [];
  for (let left = 0; left < paths.length; left += 1) {
    for (let right = left + 1; right < paths.length; right += 1) {
      const reasons = pathConflictReasons(paths[left], paths[right]);
      if (reasons.length > 0) {
        conflicts.push({ left: paths[left].pathId, right: paths[right].pathId, reasons });
      }
    }
  }
  return conflicts;
}

function pathIdList(paths: ChainEvidence[]): string[] {
  return paths.map((path) => path.pathId).sort();
}

function betterChoice(
  left: { weight: number; ids: string[] },
  right: { weight: number; ids: string[] },
): boolean {
  if (left.weight !== right.weight) return left.weight > right.weight;
  return compareStringArrays(left.ids, right.ids) < 0;
}

function solveComponent(
  candidates: ChainEvidence[],
  component: number[],
  conflictMask: bigint[],
): { weight: number; selected: number[] } {
  if (component.length === 0) return { weight: 0, selected: [] };
  const localIndex = new Map<number, number>();
  component.forEach((globalIndex, index) => localIndex.set(globalIndex, index));
  const count = component.length;
  const localMasks = component.map((globalIndex) => {
    let mask = 0n;
    for (let other = 0; other < count; other += 1) {
      if (other === localIndex.get(globalIndex)) continue;
      const target = component[other];
      if ((conflictMask[globalIndex] & (1n << BigInt(target))) !== 0n) {
        mask |= 1n << BigInt(other);
      }
    }
    return mask;
  });
  const memo = new Map<string, { weight: number; selected: number[] }>();

  const visit = (mask: bigint): { weight: number; selected: number[] } => {
    if (mask === 0n) return { weight: 0, selected: [] };
    const key = mask.toString();
    const cached = memo.get(key);
    if (cached) return cached;
    let vertex = 0;
    while (((mask >> BigInt(vertex)) & 1n) === 0n) vertex += 1;
    const without = mask & ~(1n << BigInt(vertex));
    const skip = visit(without);
    const includeRest = visit(without & ~localMasks[vertex]);
    const include = {
      weight: candidates[component[vertex]].weight + includeRest.weight,
      selected: [vertex, ...includeRest.selected],
    };
    const includeIds = include.selected.map((index) => candidates[component[index]].pathId).sort();
    const skipIds = skip.selected.map((index) => candidates[component[index]].pathId).sort();
    const best = betterChoice(
      { weight: include.weight, ids: includeIds },
      { weight: skip.weight, ids: skipIds },
    )
      ? include
      : skip;
    memo.set(key, best);
    return best;
  };

  const result = visit((1n << BigInt(count)) - 1n);
  return {
    weight: result.weight,
    selected: result.selected.map((index) => component[index]),
  };
}

export function maxIndependentEndorsements(paths: ChainEvidence[]): IndependentSelection {
  const candidates = paths
    .filter((path) => path.valid)
    .sort((left, right) => {
      if (left.weight !== right.weight) return right.weight - left.weight;
      return left.pathId < right.pathId ? -1 : left.pathId > right.pathId ? 1 : 0;
    });
  if (candidates.length === 0) return { paths: [], weight: 0, overflow: false };
  if (candidates.length > MAX_EXACT_CANDIDATES) {
    return { paths: [], weight: 0, overflow: true };
  }

  const conflictMask = new Array<bigint>(candidates.length).fill(0n);
  for (let left = 0; left < candidates.length; left += 1) {
    for (let right = left + 1; right < candidates.length; right += 1) {
      if (pathsConflict(candidates[left], candidates[right])) {
        conflictMask[left] |= 1n << BigInt(right);
        conflictMask[right] |= 1n << BigInt(left);
      }
    }
  }

  const unvisited = new Set(candidates.map((_, index) => index));
  const selected: number[] = [];
  let weight = 0;
  while (unvisited.size > 0) {
    const first = unvisited.values().next().value as number;
    const component: number[] = [];
    const queue = [first];
    unvisited.delete(first);
    while (queue.length > 0) {
      const current = queue.shift() as number;
      component.push(current);
      for (let other = 0; other < candidates.length; other += 1) {
        if (!unvisited.has(other)) continue;
        if ((conflictMask[current] & (1n << BigInt(other))) !== 0n) {
          unvisited.delete(other);
          queue.push(other);
        }
      }
    }
    const solved = solveComponent(candidates, component, conflictMask);
    weight += solved.weight;
    selected.push(...solved.selected);
  }

  const result = selected.map((index) => candidates[index]);
  result.sort((left, right) => (left.pathId < right.pathId ? -1 : left.pathId > right.pathId ? 1 : 0));
  return { paths: result, weight, overflow: false };
}

function buildCompatibleSet(paths: ChainEvidence[], maximal: boolean): CompatibleSet {
  const language = dfaUnionAll(paths.map((path) => deserializeDfa(path.language)));
  return {
    pathIds: pathIdList(paths),
    paths: [...paths].sort((left, right) => (left.pathId < right.pathId ? -1 : 1)),
    weight: paths.reduce((sum, path) => sum + path.weight, 0),
    language: serializeDfa(language),
    languageDigest: dfaDigest(language),
    intermediateDomains: uniqueSorted(paths.flatMap((path) => path.intermediateDomains)),
    revocationPredecessors: uniqueSorted(paths.flatMap((path) => path.revocationPredecessors)),
    maximal,
  };
}

function isSubset(left: string[], right: string[]): boolean {
  const rightSet = new Set(right);
  return left.every((item) => rightSet.has(item));
}

export function enumerateCompatibleEndorsementSets(
  paths: ChainEvidence[],
  options: CompatibleSetOptions = {},
): CompatibleSetEnumeration {
  const candidates = paths
    .filter((path) => (options.includeInvalid ? true : path.valid))
    .sort((left, right) => (left.pathId < right.pathId ? -1 : 1));
  const limit = options.limit ?? 100000;
  const rawSets: Array<{ paths: ChainEvidence[]; maximal: boolean }> = [];
  let truncated = false;

  const compatibleWith = (selected: ChainEvidence[], candidate: ChainEvidence): boolean =>
    selected.every((path) => !pathsConflict(path, candidate));

  const dfs = (index: number, selected: ChainEvidence[]): void => {
    if (truncated) return;
    if (rawSets.length >= limit) {
      truncated = true;
      return;
    }
    if (index >= candidates.length) {
      if (selected.length === 0) return;
      rawSets.push({ paths: [...selected], maximal: false });
      return;
    }
    const candidate = candidates[index];
    const canInclude = compatibleWith(selected, candidate);
    if (canInclude) {
      selected.push(candidate);
      dfs(index + 1, selected);
      selected.pop();
    }
    dfs(index + 1, selected);
  };

  dfs(0, []);
  const markedMaximal = rawSets.map((item) => {
    const maximal = !rawSets.some(
      (other) =>
        other.paths.length > item.paths.length &&
        isSubset(
          item.paths.map((path) => path.pathId),
          other.paths.map((path) => path.pathId),
        ),
    );
    return { ...item, maximal };
  });
  const filtered = options.maximalOnly ? markedMaximal.filter((item) => item.maximal) : markedMaximal;
  const sets = filtered.map((item) =>
    buildCompatibleSet(item.paths, item.maximal),
  );
  sets.sort((left, right) => {
    if (left.weight !== right.weight) return right.weight - left.weight;
    return compareStringArrays(left.pathIds, right.pathIds);
  });
  return { sets, truncated };
}

export function sumPathWeights(paths: ChainEvidence[]): number {
  return paths.reduce((sum, path) => sum + path.weight, 0);
}
