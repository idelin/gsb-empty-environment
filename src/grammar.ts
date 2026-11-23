import type {
  AssemblyRef,
  Grammar,
  GrammarAlt,
  GrammarProduction,
  PolicyTable,
  TypeRename,
} from './types.ts';
import { rangeListSubset } from './version.ts';

export class PolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PolicyError';
  }
}

/** A production may briefly carry an alias (single nonterminal indirection)
 *  while a grammar is being built. Alias-only cycles are illegal. */
type RawProduction = GrammarProduction & { alias?: string };

/**
 * Guardedness check.
 *
 * Edges through an `alias` are unguarded; edges reaching into a constructor's
 * arguments/elements are guarded. On every back-edge we inspect the active
 * stack segment: a cycle containing at least one constructor production is
 * protected, while a cycle made only of alias edges is a bare alias ring.
 */
export function assertGuarded(productions: RawProduction[]): void {
  const byNt = new Map(productions.map((p) => [p.nt, p]));
  const state = new Map<string, 0 | 1 | 2>();

  interface Frame {
    nt: string;
    nextIndex: number;
    successors: { nt: string; guarded: boolean }[];
  }

  for (const start of byNt.keys()) {
    if (state.get(start) === 2) continue;
    const stack: Frame[] = [makeFrame(start, byNt)];
    state.set(start, 1);
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      if (top.nextIndex >= top.successors.length) {
        state.set(top.nt, 2);
        stack.pop();
        continue;
      }
      const edge = top.successors[top.nextIndex];
      top.nextIndex += 1;
      const childState = state.get(edge.nt) ?? 0;
      if (childState === 2) continue;
      if (childState === 1) {
        if (!edge.guarded) {
          const ancestorIndex = stack.findIndex((frame) => frame.nt === edge.nt);
          const guardedOnCycle =
            edge.guarded ||
            stack.slice(ancestorIndex).some((frame) => frame.successors.some((succ) => succ.guarded));
          if (!guardedOnCycle) {
            throw new PolicyError(`bare alias ring through nonterminal ${edge.nt}`);
          }
        }
        continue;
      }
      state.set(edge.nt, 1);
      stack.push(makeFrame(edge.nt, byNt));
    }
  }
}

function makeFrame(nt: string, byNt: Map<string, RawProduction>): Frame {
  const prod = byNt.get(nt);
  if (prod === undefined) throw new PolicyError(`undefined nonterminal ${nt}`);
  if (prod.alias !== undefined) {
    return { nt, nextIndex: 0, successors: [{ nt: prod.alias, guarded: false }] };
  }
  return { nt, nextIndex: 0, successors: collectNtRefs(prod).map((ref) => ({ nt: ref, guarded: true })) };
}

function collectNtRefs(prod: GrammarProduction): string[] {
  const refs: string[] = [];
  for (const alt of prod.alts) {
    for (const arg of alt.args ?? []) refs.push(arg.nt);
    if (alt.element !== undefined) refs.push(alt.element);
  }
  return refs;
}

function exactRef(rename: TypeRename): AssemblyRef {
  return {
    name: rename.oldAssembly.name,
    publicKey: rename.oldAssembly.publicKey,
    versions: [{ min: rename.oldAssembly.version, max: rename.oldAssembly.version }],
  };
}

/** Validate the baseline grammar and return it as-is (already guarded). */
export function compileBaseline(policy: PolicyTable): Grammar {
  const productions: RawProduction[] = policy.productions.map((p) => ({ ...p }));
  assertNtCoverage(productions, policy.start);
  assertGuarded(productions);
  return {
    ruleVersion: policy.ruleVersion,
    start: policy.start,
    productions: productions.map(stripAlias),
  };
}

function stripAlias(p: RawProduction): GrammarProduction {
  const { alias: _alias, ...rest } = p;
  return rest;
}

function assertNtCoverage(productions: RawProduction[], start: string): void {
  const known = new Set(productions.map((p) => p.nt));
  if (!known.has(start)) throw new PolicyError(`start nonterminal ${start} missing`);
  for (const p of productions) {
    if (p.alias !== undefined && p.alts.length > 0) {
      throw new PolicyError(`nonterminal ${p.nt} mixes alias and alternatives`);
    }
    for (const nt of collectNtRefs(p)) {
      if (!known.has(nt)) throw new PolicyError(`dangling nonterminal ref ${nt}`);
    }
  }
}

interface RenameKey {
  publicKey: string;
  name: string | undefined;
  version: string;
  typeName: string;
}

function renameKey(
  assembly: { publicKey: string; name?: string; version: string },
  typeName: string,
): string {
  return `${assembly.publicKey}|${assembly.name ?? ''}|${assembly.version}|${typeName}`;
}

/**
 * Compile the new-deployment grammar from the baseline plus renames.
 * Rules enforced here:
 *  - renamed constructors keep the same arity and parameter variance;
 *    any variance marker change is an illegal implicit relaxation;
 *  - parameter permutations must be bijections over the arity;
 *  - new version ranges must be contained in the old accepted versions;
 *  - public keys never change silently (a renamed assembly may use a new key,
 *    but then the rename must name it explicitly and head containment is
 *    re-checked at inclusion time against the old key).
 */
export function compileNewDeployment(policy: PolicyTable): Grammar {
  const renameIndex = new Map<string, TypeRename>();
  for (const rename of policy.renames ?? []) {
    renameIndex.set(renameKey(rename.oldAssembly, rename.oldTypeName), rename);
  }

  const newProductions: RawProduction[] = policy.productions.map((prod) => {
    const alts: GrammarAlt[] = prod.alts.map((alt) => remapAlt(alt, renameIndex));
    return { nt: prod.nt, alts };
  });

  assertNtCoverage(newProductions, policy.start);
  assertGuarded(newProductions);
  return {
    ruleVersion: policy.ruleVersion,
    start: policy.start,
    productions: newProductions.map(stripAlias),
  };
}

function remapAlt(alt: GrammarAlt, renameIndex: Map<string, TypeRename>): GrammarAlt {
  const rename = findRename(alt, renameIndex);
  if (rename === undefined) return structuredClone(alt);

  const oldArgs = alt.args ?? [];
  const permutation = rename.paramPermutation ?? oldArgs.map((_, i) => i);
  assertPermutation(permutation, oldArgs.length, alt.typeName);
  const args: GrammarArg[] = permutation.map((oldPos, newPos) => {
    const source = oldArgs[oldPos];
    const declared = rename.variance?.[newPos];
    if (declared !== undefined && declared !== source.variance) {
      throw new PolicyError(
        `illegal variance relaxation for ${rename.newTypeName} position ${newPos}: ` +
          `${source.variance} -> ${declared}`,
      );
    }
    return { nt: source.nt, variance: source.variance };
  });

  const newVersions = rename.versionRange
    ? [rename.versionRange]
    : [{ min: rename.newAssembly.version, max: rename.newAssembly.version }];
  if (
    rename.oldAssembly.publicKey === rename.newAssembly.publicKey &&
    !rangeListSubset(newVersions, alt.assembly.versions)
  ) {
    throw new PolicyError(
      `version range for ${rename.newTypeName} widens beyond the old binding table`,
    );
  }

  return {
    assembly: {
      name: rename.newAssembly.name,
      publicKey: rename.newAssembly.publicKey,
      versions: newVersions,
    },
    typeName: rename.newTypeName,
    arrayRank: alt.arrayRank,
    args,
    element: alt.element,
  };
}

function findRename(
  alt: GrammarAlt,
  renameIndex: Map<string, TypeRename>,
): TypeRename | undefined {
  for (const range of alt.assembly.versions) {
    const versions = enumerateRangeEndpoints(range);
    for (const version of versions) {
      const hit = renameIndex.get(
        renameKey(
          { publicKey: alt.assembly.publicKey, name: alt.assembly.name, version },
          alt.typeName,
        ),
      );
      if (hit !== undefined) return hit;
    }
  }
  return undefined;
}

/** Endpoints are enough to key renames: a range is checked for containment
 *  separately, and renames name a concrete old assembly version. */
function enumerateRangeEndpoints(range: { min?: string; max?: string }): string[] {
  const points: string[] = [];
  if (range.min !== undefined) points.push(range.min);
  if (range.max !== undefined && range.max !== range.min) points.push(range.max);
  return points;
}

function assertPermutation(permutation: number[], arity: number, typeName: string): void {
  if (permutation.length !== arity) {
    throw new PolicyError(`permutation arity mismatch for ${typeName}`);
  }
  const seen = new Set<number>();
  for (const value of permutation) {
    if (!Number.isInteger(value) || value < 0 || value >= arity || seen.has(value)) {
      throw new PolicyError(`permutation for ${typeName} is not a bijection`);
    }
    seen.add(value);
  }
}
