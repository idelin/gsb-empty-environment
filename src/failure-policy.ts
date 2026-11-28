import type {
  ContainerState,
  PodCondition,
  PodFailureInfo,
  PodFailurePolicy,
  PodFailurePolicyOnExitCodes,
  PodFailurePolicyOnPodCondition,
  PodFailurePolicyRule,
} from './types.js';

/** The rule selected for a failed pod. */
export interface MatchedRule {
  /** Zero-based position of the rule inside `podFailurePolicy.rules`. */
  ruleIndex: number;
  rule: PodFailurePolicyRule;
}

/**
 * Evaluate an `onExitCodes` requirement against the failed pod.
 *
 * Only terminated containers with a non-zero exit code are considered, which
 * matches the Kubernetes semantics where successful (zero) exit codes and
 * still-running containers never trigger an exit-code rule.
 */
export function matchesExitCodes(
  requirement: PodFailurePolicyOnExitCodes,
  containers: ContainerState[],
): boolean {
  const inspected = requirement.containerName
    ? containers.filter((container) => container.name === requirement.containerName)
    : containers;

  const failedExitCodes = inspected
    .map((container) => container.exitCode)
    .filter((code): code is number => typeof code === 'number' && code !== 0);

  if (failedExitCodes.length === 0) {
    return false;
  }

  const wanted = new Set(requirement.values);
  switch (requirement.operator) {
    case 'In':
      return failedExitCodes.some((code) => wanted.has(code));
    case 'NotIn':
      return failedExitCodes.every((code) => !wanted.has(code));
    default:
      return false;
  }
}

/** Evaluate one pod-condition pattern against the pod's current conditions. */
export function matchesPodCondition(
  pattern: PodFailurePolicyOnPodCondition,
  conditions: PodCondition[],
): boolean {
  const expectedStatus = pattern.status ?? 'True';
  const operator = pattern.operator ?? 'In';
  const found = conditions.find((condition) => condition.type === pattern.type);

  if (operator === 'NotIn') {
    // NotIn matches when the condition is absent, or present with another status.
    return found === undefined || found.status !== expectedStatus;
  }
  // In matches only when the condition exists with the expected status.
  return found !== undefined && found.status === expectedStatus;
}

/**
 * Decide whether a whole rule matches. Every requirement that is present on
 * the rule must match (AND semantics). A rule without any requirement is a
 * catch-all that always matches.
 */
export function ruleMatches(
  rule: PodFailurePolicyRule,
  failure: PodFailureInfo,
): boolean {
  if (rule.onExitCodes !== undefined) {
    if (!matchesExitCodes(rule.onExitCodes, failure.containers)) {
      return false;
    }
  }

  if (rule.onPodConditions !== undefined && rule.onPodConditions.length > 0) {
    const conditions = failure.conditions ?? [];
    const allConditionsMatch = rule.onPodConditions.every((pattern) =>
      matchesPodCondition(pattern, conditions),
    );
    if (!allConditionsMatch) {
      return false;
    }
  }

  return true;
}

/**
 * Walk `podFailurePolicy.rules` in declaration order and return the first
 * matching rule. Returns `undefined` when no rule matches (or when the policy
 * is absent), which makes the caller fall back to the default `Count` path.
 */
export function evaluateFailurePolicy(
  policy: PodFailurePolicy | undefined,
  failure: PodFailureInfo,
): MatchedRule | undefined {
  if (policy === undefined) {
    return undefined;
  }
  for (let ruleIndex = 0; ruleIndex < policy.rules.length; ruleIndex += 1) {
    const rule = policy.rules[ruleIndex];
    if (ruleMatches(rule, failure)) {
      return { ruleIndex, rule };
    }
  }
  return undefined;
}
