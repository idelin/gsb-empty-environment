import type { LabelSelector, LabelSelectorRequirement } from './types.ts';

function matchesRequirement(
  labels: Record<string, string>,
  requirement: LabelSelectorRequirement,
): boolean {
  const present = Object.prototype.hasOwnProperty.call(labels, requirement.key);
  switch (requirement.operator) {
    case 'Exists':
      return present;
    case 'DoesNotExist':
      return !present;
    case 'In':
      return present && (requirement.values ?? []).includes(labels[requirement.key]);
    case 'NotIn':
      return !present || !(requirement.values ?? []).includes(labels[requirement.key]);
    default:
      return false;
  }
}

/** 按 Kubernetes label selector 语义判定命名空间标签是否满足条件。 */
export function matchesLabelSelector(
  labels: Record<string, string>,
  selector: LabelSelector | undefined,
): boolean {
  if (!selector) {
    return true;
  }
  for (const [key, value] of Object.entries(selector.matchLabels ?? {})) {
    if (labels[key] !== value) {
      return false;
    }
  }
  for (const requirement of selector.matchExpressions ?? []) {
    if (!matchesRequirement(labels, requirement)) {
      return false;
    }
  }
  return true;
}
