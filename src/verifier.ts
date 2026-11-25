import type {
  AttributeRequirement,
  AttributeWitness,
  Diagnostic,
  DistributionBundle,
  EvaluationReport,
  SignatureRecord,
  VerifiedSignature,
} from "./types.ts";
import { coversAll, isValidRange, validateCanonicalMap } from "./ranges.ts";

/**
 * Coverage-acceptance verifier.
 *
 * Structural defects (unknown algorithm, unbound schema, ambiguous reverse
 * map, binding mismatches) are reported per record with distinct diagnostic
 * codes. Records that survive structural checks are sent to the crypto
 * backend at most once each, so verification-call count equals the number of
 * distinct records whose signature math is actually exercised.
 */
export class Verifier {
  private readonly crypto: CryptoBackendLike;

  constructor(crypto: CryptoBackendLike) {
    this.crypto = crypto;
  }

  evaluate(bundle: DistributionBundle): EvaluationReport {
    const diagnostics: Diagnostic[] = [];
    const manifest = bundle.manifest;

    // 1. Host reverse maps are validated before any signature work so an
    //    ambiguous mapping is reported even if no record references it.
    for (const [canonicalId, spec] of Object.entries(manifest.canonicalizations)) {
      const result = validateCanonicalMap(spec);
      if (!result.ok) {
        diagnostics.push({
          code: "AMBIGUOUS_REVERSE_MAP",
          message: result.reason,
          canonicalId,
        });
      }
    }

    const ambiguous = new Set(
      diagnostics
        .filter((diagnostic) => diagnostic.code === "AMBIGUOUS_REVERSE_MAP")
        .map((diagnostic) => diagnostic.canonicalId),
    );

    // 2. Per-record structural gating.
    const accepted: VerifiedSignature[] = [];
    const rejected = new Set<number>();
    for (const record of bundle.records) {
      if (this.reject(record, bundle, ambiguous, diagnostics)) {
        rejected.add(record.index);
        continue;
      }
      accepted.push(toVerified(record));
    }

    // 3. Crypto verification: one backend call per structurally valid record.
    const cryptoFailures = new Set<number>();
    for (const record of bundle.records) {
      if (rejected.has(record.index)) continue;
      if (!this.crypto.verify(record, bundle)) {
        cryptoFailures.add(record.index);
        diagnostics.push({
          code: "CRYPTO_FAILURE",
          message: `record ${record.index} failed cryptographic verification`,
          recordIndex: record.index,
        });
      }
    }
    const verified = accepted.filter((signature) => !cryptoFailures.has(signature.index));

    // 4. Minimum global witness per required attribute.
    const witnesses = manifest.requirements.map((requirement) =>
      this.buildWitness(requirement, verified, bundle),
    );

    return {
      diagnostics,
      verified,
      witnesses,
      totalVerificationCalls: bundle.records.filter(
        (record) => !rejected.has(record.index),
      ).length,
    };
  }

  private reject(
    record: SignatureRecord,
    bundle: DistributionBundle,
    ambiguous: Set<string | undefined>,
    diagnostics: Diagnostic[],
  ): boolean {
    const manifest = bundle.manifest;
    const push = (code: Diagnostic["code"], message: string) => {
      diagnostics.push({ code, message, recordIndex: record.index });
    };

    if (!this.crypto.knownAlgorithms.has(record.algorithm)) {
      push("UNKNOWN_ALGORITHM", `algorithm "${record.algorithm}" is not registered`);
      return true;
    }
    if (!isValidRange(record.coveredRange, bundle.rawBytes.length)) {
      push("INVALID_RANGE", "covered range is not a half-open interval of the package");
      return true;
    }
    if (record.packageId !== manifest.packageId) {
      push("PACKAGE_ID_MISMATCH", `record binds "${record.packageId}", package is "${manifest.packageId}"`);
      return true;
    }
    const canonicalId = manifest.schemaBindings[record.payloadSchema];
    if (canonicalId === undefined) {
      push("UNBOUND_SCHEMA", `payload schema "${record.payloadSchema}" has no canonicalization binding`);
      return true;
    }
    if (ambiguous.has(canonicalId)) {
      // Already reported at the canonicalization level; record cannot be used.
      return true;
    }
    const spec = manifest.canonicalizations[canonicalId];
    if (spec.version !== record.canonicalVersion) {
      push(
        "CANONICAL_VERSION_MISMATCH",
        `record pins canonical version "${record.canonicalVersion}", current is "${spec.version}"`,
      );
      return true;
    }
    if (!scopeMatches(record, bundle)) {
      push("CANONICAL_SCOPE_MISMATCH", `record scope "${record.scope}" does not match the regions it covers`);
      return true;
    }
    return false;
  }

  /**
   * Exhaustive subset enumeration over the small package. Candidates are the
   * records that individually pass the same-signature endorsement test. We
   * enumerate by increasing subset size, and within a size by ascending index
   * vector, so the first feasible subset is the minimum-call witness with the
   * required record-index tie break.
   */
  private buildWitness(
    requirement: AttributeRequirement,
    verified: VerifiedSignature[],
    bundle: DistributionBundle,
  ): AttributeWitness {
    const lengthRanges = requirement.lengthFieldIds.map((id) => {
      const field = bundle.manifest.lengthFields.find((candidate) => candidate.id === id);
      if (!field) throw new Error(`unknown length field "${id}" in requirement`);
      return field.range;
    });
    const needed = [...requirement.encodingRanges, ...lengthRanges];

    const candidates = verified
      .filter((signature) => signature.declaredAttributes.includes(requirement.attribute))
      .filter((signature) => coversAll(signature.coveredRange, needed))
      .sort((a, b) => a.index - b.index);

    if (candidates.length === 0) {
      return unsatisfied(requirement, "no single signature covers the full raw encoding and every influencing length field");
    }

    for (const subset of enumerateSubsets(candidates)) {
      if (this.feasible(subset, requirement, bundle)) {
        const indices = subset.map((signature) => signature.index);
        return {
          attribute: requirement.attribute,
          recordIndices: indices,
          verificationCalls: indices.length,
          distinctKeyMaterials: new Set(subset.map((signature) => signature.keyMaterialId)).size,
          satisfied: true,
        };
      }
    }

    const reason = thresholdFailure(candidates, requirement)
      ? "distinct public-key material threshold cannot be met"
      : "management-domain exclusion rules forbid any feasible combination";
    return unsatisfied(requirement, reason);
  }

  private feasible(
    subset: VerifiedSignature[],
    requirement: AttributeRequirement,
    bundle: DistributionBundle,
  ): boolean {
    if (!withinDomainPolicy(subset, bundle.manifest.exclusionRules)) return false;
    const distinctMaterials = new Set(subset.map((signature) => signature.keyMaterialId)).size;
    return distinctMaterials >= requirement.distinctKeyMaterials;
  }
}

interface CryptoBackendLike {
  knownAlgorithms: ReadonlySet<string>;
  verify(record: SignatureRecord, bundle: DistributionBundle): boolean;
}

function toVerified(record: SignatureRecord): VerifiedSignature {
  return {
    index: record.index,
    keyMaterialId: record.keyMaterialId,
    domain: record.domain,
    scope: record.scope,
    coveredRange: record.coveredRange,
    declaredAttributes: record.declaredAttributes,
  };
}

/**
 * Scope binding check. A record declaring scope S is valid only when it
 * substantially covers the region in S and does not substantially cover a
 * region in another scope. Aliased bytes (same raw range, second scope) are
 * therefore only usable by a record explicitly declaring the alias scope.
 */
function scopeMatches(record: SignatureRecord, bundle: DistributionBundle): boolean {
  const touched = bundle.manifest.regions.filter(
    (region) => overlapLength(record.coveredRange, region.range) > 1,
  );
  if (touched.length === 0) return false;
  // Duplicate aliases share raw bytes but live in another scope; commitments
  // under different scopes can never be reused, so every touched region must
  // carry exactly the scope the record declares.
  return touched.every((region) => region.scope === record.scope);
}

function overlapLength(a: { start: number; end: number }, b: { start: number; end: number }): number {
  return Math.max(0, Math.min(a.end, b.end) - Math.max(a.start, b.start));
}

function withinDomainPolicy(
  subset: VerifiedSignature[],
  rules: DistributionBundle["manifest"]["exclusionRules"],
): boolean {
  const domains = new Set(subset.map((signature) => signature.domain));
  return rules.every(
    (rule) => !(domains.has(rule.domainA) && domains.has(rule.domainB)),
  );
}

function thresholdFailure(
  candidates: VerifiedSignature[],
  requirement: AttributeRequirement,
): boolean {
  const available = new Set(candidates.map((signature) => signature.keyMaterialId)).size;
  return available < requirement.distinctKeyMaterials;
}

function unsatisfied(
  requirement: AttributeRequirement,
  reason: string,
): AttributeWitness {
  return {
    attribute: requirement.attribute,
    recordIndices: [],
    verificationCalls: 0,
    distinctKeyMaterials: 0,
    satisfied: false,
    reason,
  };
}

/**
 * Generate subsets in (size, lexicographic index vector) order. The input is
 * already index-sorted, so bitmask enumeration within each size naturally
 * yields the ascending-index tie break the acceptance rules demand.
 */
export function* enumerateSubsets<T>(items: T[]): Generator<T[]> {
  const total = items.length;
  for (let size = 1; size <= total; size += 1) {
    for (let mask = 0; mask < 1 << total; mask += 1) {
      if (popCount(mask) !== size) continue;
      const subset: T[] = [];
      for (let bit = 0; bit < total; bit += 1) {
        if (mask & (1 << bit)) subset.push(items[bit]);
      }
      yield subset;
    }
  }
}

function popCount(value: number): number {
  let count = 0;
  let remaining = value;
  while (remaining) {
    count += remaining & 1;
    remaining >>= 1;
  }
  return count;
}
