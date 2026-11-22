import { areDisjointDFA, pathToString, pathsDFA } from "./dfa.ts";
import { resolveEffectivePolicy, validateProvenanceChain } from "./resolve.ts";
import type {
  ChangeAnalysis,
  ChangeAsset,
  ChangeClassification,
  ChangePlan,
  ChangeRegion,
  ConstraintDelta,
  ConstraintValue,
  EffectiveConstraint,
  EffectivePolicy,
  PolicyState,
  TrustOptions,
  ValidatedProvenanceChain,
} from "./types.ts";

function sameSet(left: readonly ConstraintValue[], right: readonly ConstraintValue[]): boolean {
  const a = new Set(left);
  const b = new Set(right);
  if (a.size !== b.size) {
    return false;
  }
  for (const value of a) {
    if (!b.has(value)) {
      return false;
    }
  }
  return true;
}

function subset(left: readonly ConstraintValue[], right: readonly ConstraintValue[]): boolean {
  const other = new Set(right);
  return left.every((value) => other.has(value));
}

function constraintDelta(
  key: string,
  before: EffectiveConstraint | null,
  after: EffectiveConstraint | null,
): ConstraintDelta {
  if (before?.status === "conflict" || after?.status === "conflict") {
    return { key, before, after, classification: "conflict", relation: "conflict" };
  }
  if (!before && after) {
    return { key, before, after, classification: "tightened", relation: "added" };
  }
  if (before && !after) {
    return { key, before, after, classification: "loosened", relation: "removed" };
  }
  if (!before || !after) {
    return { key, before, after, classification: "unchanged", relation: "same" };
  }
  if (sameSet(before.allowed, after.allowed)) {
    return { key, before, after, classification: "unchanged", relation: "same" };
  }
  if (subset(after.allowed, before.allowed)) {
    return { key, before, after, classification: "tightened", relation: "strict" };
  }
  if (subset(before.allowed, after.allowed)) {
    return { key, before, after, classification: "loosened", relation: "permissive" };
  }
  return { key, before, after, classification: "mixed", relation: "incomparable" };
}

function combineClassification(
  classifications: readonly ChangeClassification[],
): ChangeClassification {
  if (classifications.length === 0) {
    return "unchanged";
  }
  if (classifications.includes("unknown")) {
    return "unknown";
  }
  if (classifications.includes("conflict")) {
    return "conflict";
  }
  if (classifications.includes("mixed")) {
    return "mixed";
  }
  const tightened = classifications.includes("tightened");
  const loosened = classifications.includes("loosened");
  if (tightened && loosened) {
    return "mixed";
  }
  if (tightened) {
    return "tightened";
  }
  if (loosened) {
    return "loosened";
  }
  return "unchanged";
}

export interface ClassifiedChange {
  classification: ChangeClassification;
  deltas: ConstraintDelta[];
  unknownOnlyForProof: boolean;
}

export function classifyPolicyChange(
  before: EffectivePolicy,
  after: EffectivePolicy,
): ClassifiedChange {
  const keys = new Set([...Object.keys(before.constraints), ...Object.keys(after.constraints)]);
  const deltas = [...keys]
    .sort()
    .map((key) =>
      constraintDelta(
        key,
        before.constraints[key] ?? null,
        after.constraints[key] ?? null,
      ),
    );
  const unknownReasons = [...before.unknown, ...after.unknown];
  const unknownOnlyForProof =
    unknownReasons.length > 0 &&
    unknownReasons.every((reason) => reason.code === "missing-membership-proof");

  if (before.status === "unknown" || after.status === "unknown") {
    return {
      classification: "unknown",
      deltas,
      unknownOnlyForProof,
    };
  }
  if (before.status === "conflict" || after.status === "conflict") {
    return {
      classification: "conflict",
      deltas,
      unknownOnlyForProof: false,
    };
  }
  return {
    classification: combineClassification(deltas.map((delta) => delta.classification)),
    deltas,
    unknownOnlyForProof: false,
  };
}

interface AssetRun {
  asset: ChangeAsset;
  before: EffectivePolicy;
  after: EffectivePolicy;
  classified: ClassifiedChange;
  pathKey: string;
}

function runAsset(plan: ChangePlan, asset: ChangeAsset): AssetRun {
  const beforeTarget = asset.before.target ?? plan.target;
  const afterTarget = asset.after.target ?? plan.target;
  if (!beforeTarget || !afterTarget) {
    throw new Error(`Asset ${asset.id} needs before/after target nodes`);
  }
  const before = resolveEffectivePolicy({
    state: plan.before,
    target: beforeTarget,
    identity: asset.before.identity,
    membershipProofs: asset.before.membershipProofs,
    trust: plan.trust,
    maxProvenanceChains: plan.maxProvenanceChains,
  });
  const after = resolveEffectivePolicy({
    state: plan.after,
    target: afterTarget,
    identity: asset.after.identity,
    membershipProofs: asset.after.membershipProofs,
    trust: plan.trust,
    maxProvenanceChains: plan.maxProvenanceChains,
  });
  return {
    asset,
    before,
    after,
    classified: classifyPolicyChange(before, after),
    pathKey: pathToString(asset.after.identity.path),
  };
}

function mergeDeltas(runs: readonly AssetRun[]): ConstraintDelta[] {
  const byKey = new Map<string, ConstraintDelta>();
  for (const run of runs) {
    for (const delta of run.classified.deltas) {
      if (!byKey.has(delta.key)) {
        byKey.set(delta.key, delta);
      }
    }
  }
  return [...byKey.values()].sort((a, b) => a.key.localeCompare(b.key));
}

function collectChains(
  runs: readonly AssetRun[],
  side: "before" | "after",
  state: PolicyState,
  trust?: TrustOptions,
): ValidatedProvenanceChain[] {
  const result: ValidatedProvenanceChain[] = [];
  const seen = new Set<string>();
  for (const run of runs) {
    const policy = side === "before" ? run.before : run.after;
    for (const constraint of Object.values(policy.constraints)) {
      for (const origin of constraint.origins) {
        for (const chain of origin.chains) {
          const candidate: ValidatedProvenanceChain = {
            ...chain,
            side,
            assetId: run.asset.id,
            valid: true,
          };
          const key = JSON.stringify(candidate);
          if (seen.has(key)) {
            continue;
          }
          if (
            validateProvenanceChain(state.graph, chain, policy.target, {
              at: state.at,
              trust,
            })
          ) {
            seen.add(key);
            result.push(candidate);
          }
        }
      }
    }
  }
  return result;
}

export function analyzePolicyChange(plan: ChangePlan): ChangeAnalysis {
  if (plan.assets.length === 0) {
    return {
      regions: [],
      disjoint: true,
      chains: [],
      before: [],
      after: [],
    };
  }
  const runs = plan.assets.map((asset) => runAsset(plan, asset));
  const groups = new Map<string, AssetRun[]>();
  for (const run of runs) {
    const list = groups.get(run.pathKey) ?? [];
    list.push(run);
    groups.set(run.pathKey, list);
  }

  const regions: ChangeRegion[] = [];
  let index = 0;
  for (const [pathKey, group] of [...groups.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    const classification = combineClassification(
      group.map((run) => run.classified.classification),
    );
    const unknown = [...new Set(
      group.flatMap((run) => [...run.before.unknown, ...run.after.unknown]).map((reason) =>
        JSON.stringify(reason),
      ),
    )]
      .map((json) => JSON.parse(json) as ChangeRegion["unknown"][number]);
    const path = group[0]?.asset.after.identity.path ?? [];
    const unknownOnlyForProof =
      classification === "unknown" &&
      unknown.length > 0 &&
      unknown.every((reason) => reason.code === "missing-membership-proof");
    regions.push({
      id: `region-${index}-${classification}`,
      classification,
      path: pathsDFA([path]),
      unknownOnlyForProof,
      assetIds: group.map((run) => run.asset.id),
      paths: [path],
      deltas: mergeDeltas(group),
      unknown,
    });
    index += 1;
  }

  let disjoint = true;
  for (let left = 0; left < regions.length; left += 1) {
    for (let right = left + 1; right < regions.length; right += 1) {
      if (!areDisjointDFA(regions[left].path, regions[right].path)) {
        disjoint = false;
      }
    }
  }

  const chains = [
    ...collectChains(runs, "before", plan.before, plan.trust),
    ...collectChains(runs, "after", plan.after, plan.trust),
  ];
  return {
    regions,
    disjoint,
    chains,
    before: runs.map((run) => run.before),
    after: runs.map((run) => run.after),
  };
}

export const diffEffectivePolicies = analyzePolicyChange;
export const analyzeChangePlan = analyzePolicyChange;
export const diffPolicies = analyzePolicyChange;
