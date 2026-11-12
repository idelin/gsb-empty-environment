import { TrustChainError } from './errors.ts';
import type { MetadataPolicy, PolicyOperators } from './types.ts';

/** Structural equality good enough for policy values (JSON data). */
function jsonEqual(left: unknown, right: unknown): boolean {
  if (left === right) {
    return true;
  }
  if (
    typeof left === 'object' &&
    left !== null &&
    typeof right === 'object' &&
    right !== null
  ) {
    const leftKeys = Object.keys(left);
    const rightKeys = Object.keys(right);
    if (leftKeys.length !== rightKeys.length) {
      return false;
    }
    return leftKeys.every((key) =>
      jsonEqual(
        (left as Record<string, unknown>)[key],
        (right as Record<string, unknown>)[key],
      ),
    );
  }
  return false;
}

/** True when every element of `subset` appears in `superset`. */
export function isSubsetOf(subset: unknown[], superset: unknown[]): boolean {
  return subset.every((candidate) =>
    superset.some((allowed) => jsonEqual(candidate, allowed)),
  );
}

/** Intersect two arrays keeping the order of the first one. */
function intersect(left: unknown[], right: unknown[]): unknown[] {
  return left.filter((value) =>
    right.some((candidate) => jsonEqual(value, candidate)),
  );
}

/**
 * Merge a subordinate's policy operators into the running combined policy.
 *
 * The combined policy flows from the trust anchor downward. Each further
 * Subordinate Statement may only narrow it:
 *
 * - `subset_of` must be a subset of the running `subset_of` (or, when no
 *   running restriction exists yet, it becomes the new restriction);
 * - `one_of` and `value` must choose values still allowed by the running
 *   `subset_of`;
 * - `add`/`default` cannot introduce members that the running `subset_of`
 *   forbids.
 *
 * Any violation means the subordinate is loosening the policy and the
 * chain must be rejected.
 */
function mergeOperators(
  combined: PolicyOperators,
  incoming: PolicyOperators,
  path: string,
): PolicyOperators {
  const next: PolicyOperators = { ...combined };

  if (incoming.subset_of !== undefined) {
    if (!Array.isArray(incoming.subset_of)) {
      throw new TrustChainError(
        'POLICY_LOOSENED',
        `metadata_policy ${path}.subset_of must be an array`,
      );
    }
    if (combined.subset_of !== undefined) {
      if (!isSubsetOf(incoming.subset_of, combined.subset_of)) {
        throw new TrustChainError(
          'POLICY_LOOSENED',
          `metadata_policy ${path}.subset_of widens the allowed set; ` +
            'a subordinate subset_of must be a subset of its superior subset_of',
        );
      }
      next.subset_of = intersect(combined.subset_of, incoming.subset_of);
    } else {
      next.subset_of = [...incoming.subset_of];
    }
  }

  if (incoming.one_of !== undefined) {
    if (!Array.isArray(incoming.one_of) || incoming.one_of.length === 0) {
      throw new TrustChainError(
        'POLICY_LOOSENED',
        `metadata_policy ${path}.one_of must be a non-empty array`,
      );
    }
    if (
      next.subset_of !== undefined &&
      !isSubsetOf(incoming.one_of, next.subset_of)
    ) {
      throw new TrustChainError(
        'POLICY_LOOSENED',
        `metadata_policy ${path}.one_of contains values outside the ` +
          'running subset_of restriction',
      );
    }
    next.one_of = [...incoming.one_of];
  }

  if (incoming.value !== undefined) {
    if (
      next.subset_of !== undefined &&
      !isSubsetOf([incoming.value], next.subset_of)
    ) {
      throw new TrustChainError(
        'POLICY_LOOSENED',
        `metadata_policy ${path}.value is outside the running subset_of restriction`,
      );
    }
    next.value = incoming.value;
  }

  if (incoming.add !== undefined) {
    if (!Array.isArray(incoming.add)) {
      throw new TrustChainError(
        'POLICY_LOOSENED',
        `metadata_policy ${path}.add must be an array`,
      );
    }
    if (
      next.subset_of !== undefined &&
      !isSubsetOf(incoming.add, next.subset_of)
    ) {
      throw new TrustChainError(
        'POLICY_LOOSENED',
        `metadata_policy ${path}.add introduces members forbidden by the ` +
          'running subset_of restriction',
      );
    }
    next.add = incoming.add;
  }

  if (incoming.default !== undefined) {
    if (
      next.subset_of !== undefined &&
      !isSubsetOf([incoming.default], next.subset_of)
    ) {
      throw new TrustChainError(
        'POLICY_LOOSENED',
        `metadata_policy ${path}.default is outside the running subset_of restriction`,
      );
    }
    next.default = incoming.default;
  }

  return next;
}

/**
 * Fold the metadata policies of the Subordinate Statements into one
 * combined policy, anchor-first. Throws {@link TrustChainError} with code
 * `POLICY_LOOSENED` as soon as a subordinate attempts to widen any claim.
 *
 * @param subordinatePolicies policies ordered leaf-side first (chain order);
 *        they are applied in reverse so the trust anchor's policy is the
 *        starting restriction.
 */
export function combineMetadataPolicies(
  subordinatePolicies: MetadataPolicy[],
): MetadataPolicy {
  const combined: MetadataPolicy = {};

  for (const policy of [...subordinatePolicies].reverse()) {
    for (const [metadataType, claims] of Object.entries(policy)) {
      if (typeof claims !== 'object' || claims === null) {
        throw new TrustChainError(
          'POLICY_LOOSENED',
          `metadata_policy.${metadataType} must be an object of claim policies`,
        );
      }
      combined[metadataType] ??= {};
      for (const [claim, operators] of Object.entries(claims)) {
        const path = `${metadataType}.${claim}`;
        if (typeof operators !== 'object' || operators === null) {
          throw new TrustChainError(
            'POLICY_LOOSENED',
            `metadata_policy ${path} must be an object of operators`,
          );
        }
        combined[metadataType][claim] = mergeOperators(
          combined[metadataType][claim] ?? {},
          operators,
          path,
        );
      }
    }
  }

  return combined;
}
