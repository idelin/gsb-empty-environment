import type { LabelSelector } from './types.js';

/**
 * Evaluate a metav1.LabelSelector-compatible selector against a label set.
 * An empty/undefined selector matches every object (Kubernetes semantics).
 */
export function matchesSelector(
  selector: LabelSelector | undefined,
  labels: Record<string, string>,
): boolean {
  if (selector === undefined) {
    return true;
  }

  if (selector.matchLabels) {
    for (const [key, expected] of Object.entries(selector.matchLabels)) {
      if (labels[key] !== expected) {
        return false;
      }
    }
  }

  if (selector.matchExpressions) {
    for (const requirement of selector.matchExpressions) {
      if (!matchesRequirement(requirement, labels)) {
        return false;
      }
    }
  }

  return true;
}

function matchesRequirement(
  requirement: NonNullable<LabelSelector['matchExpressions']>[number],
  labels: Record<string, string>,
): boolean {
  const present = Object.prototype.hasOwnProperty.call(labels, requirement.key);
  const value = labels[requirement.key];

  switch (requirement.operator) {
    case 'In':
      return present && (requirement.values ?? []).includes(value as string);
    case 'NotIn':
      return !present || !(requirement.values ?? []).includes(value as string);
    case 'Exists':
      return present;
    case 'DoesNotExist':
      return !present;
    case 'Gt':
    case 'Lt': {
      const bound = Number((requirement.values ?? [])[0]);
      if (!Number.isFinite(bound) || !present) {
        return false;
      }
      const actual = Number(value);
      if (!Number.isFinite(actual)) {
        return false;
      }
      return requirement.operator === 'Gt' ? actual > bound : actual < bound;
    }
    default: {
      const exhaustive: never = requirement.operator;
      throw new Error(`unknown selector operator: ${String(exhaustive)}`);
    }
  }
}
