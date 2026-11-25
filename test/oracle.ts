import assert from "node:assert/strict";
import type {
  AttributeRequirement,
  DistributionBundle,
  EvaluationReport,
  VerifiedSignature,
} from "../src/types.ts";

/**
 * Independent acceptance oracle written only from the task contract. It does
 * not import the solver under test: coverage, thresholds, domain exclusion and
 * subset search are re-implemented naively here so the tests compare two
 * separate constructions rather than the code against itself.
 */

function containsAll(outer: { start: number; end: number }, ranges: Array<{ start: number; end: number }>): boolean {
  return ranges.every((range) => outer.start <= range.start && range.end <= outer.end);
}

export function endorsementCandidates(
  requirement: AttributeRequirement,
  verified: VerifiedSignature[],
  bundle: DistributionBundle,
): VerifiedSignature[] {
  const lengthRanges = requirement.lengthFieldIds.map((id) => {
    const field = bundle.manifest.lengthFields.find((candidate) => candidate.id === id);
    assert.ok(field, `oracle: length field ${id} missing`);
    return field!.range;
  });
  const needed = [...requirement.encodingRanges, ...lengthRanges];
  return verified.filter(
    (signature) =>
      signature.declaredAttributes.includes(requirement.attribute) &&
      containsAll(signature.coveredRange, needed),
  );
}

function domainAllowed(subset: VerifiedSignature[], bundle: DistributionBundle): boolean {
  const domains = new Set(subset.map((signature) => signature.domain));
  return bundle.manifest.exclusionRules.every(
    (rule) => !(domains.has(rule.domainA) && domains.has(rule.domainB)),
  );
}

function feasible(
  subset: VerifiedSignature[],
  requirement: AttributeRequirement,
  bundle: DistributionBundle,
): boolean {
  if (!domainAllowed(subset, bundle)) return false;
  const materials = new Set(subset.map((signature) => signature.keyMaterialId));
  return materials.size >= requirement.distinctKeyMaterials;
}

/** Brute-force minimum witness: smallest subset, ties broken by index vector. */
export function oracleWitness(
  requirement: AttributeRequirement,
  bundle: DistributionBundle,
  report: EvaluationReport,
): number[] {
  const candidates = endorsementCandidates(requirement, report.verified, bundle).sort(
    (a, b) => a.index - b.index,
  );
  const allMasks = 2 ** candidates.length;
  // Size first, then ascending lexicographic index vector: mask order alone
  // mixes sizes (e.g. mask 4 = [2] follows mask 3 = [0,1]).
  for (let size = 1; size <= candidates.length; size += 1) {
    for (let mask = 1; mask < allMasks; mask += 1) {
      if (popCount(mask) !== size) continue;
      const subset = candidates.filter((_, bit) => mask & (1 << bit));
      if (!feasible(subset, requirement, bundle)) continue;
      return subset.map((signature) => signature.index);
    }
  }
  return [];
}

function popCount(mask: number): number {
  let count = 0;
  let remaining = mask;
  while (remaining) {
    count += remaining & 1;
    remaining >>= 1;
  }
  return count;
}

/** Assert every required attribute's solver witness equals the oracle's. */
export function assertWitnessesMatchOracle(
  bundle: DistributionBundle,
  report: EvaluationReport,
): void {
  for (const requirement of bundle.manifest.requirements) {
    const expected = oracleWitness(requirement, bundle, report);
    const witness = report.witnesses.find((entry) => entry.attribute === requirement.attribute);
    assert.ok(witness, `oracle: missing witness for ${requirement.attribute}`);
    assert.deepEqual(witness!.recordIndices, expected);
    assert.equal(witness!.satisfied, expected.length > 0);
    if (expected.length > 0) {
      assert.equal(witness!.verificationCalls, expected.length);
      assert.equal(
        witness!.distinctKeyMaterials,
        new Set(
          report.verified
            .filter((signature) => expected.includes(signature.index))
            .map((signature) => signature.keyMaterialId),
        ).size,
      );
    }
  }
}

export function allSubsets<T>(items: T[]): T[][] {
  const result: T[][] = [];
  for (let mask = 0; mask < 2 ** items.length; mask += 1) {
    result.push(items.filter((_, bit) => mask & (1 << bit)));
  }
  return result;
}
