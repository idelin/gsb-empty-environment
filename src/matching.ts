import {
  AdmissionRequest,
  GroupVersionResource,
  LabelSelector,
  LabelSelectorRequirement,
  MatchResources,
  NamedRuleWithOperations,
  Operation,
} from './types.ts';

function patternMatch(pattern: string, value: string): boolean {
  if (pattern === '*') return true;
  if (pattern.endsWith('/*')) {
    // "pods/*" matches any subresource of resource "pods" but never the parent resource.
    const parent = pattern.slice(0, -2);
    const slash = value.indexOf('/');
    if (slash === -1) return false;
    return value.slice(0, slash) === parent;
  }
  if (pattern.includes('*')) {
    // Generic wildcard with a single '*' segment.
    const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*');
    return new RegExp(`^${escaped}$`).test(value);
  }
  return pattern === value;
}

function listPatternMatch(patterns: string[] | undefined, value: string): boolean {
  if (!patterns || patterns.length === 0) return true;
  return patterns.some((pattern) => patternMatch(pattern, value));
}

/**
 * Evaluate a single NamedRuleWithOperations.
 *
 * Subresource semantics (the most error-prone part of VAP matching):
 *  - resource "pods" with no subresource matches ONLY the parent resource ("pods").
 *  - resource "pods/*" matches "pods/status" but NOT the parent "pods".
 *  - resource "*" matches both parent resources and every subresource.
 *  - explicit subresource patterns (e.g. a star-slash-status style pattern) are supported.
 */
export function ruleMatchesResource(
  rule: NamedRuleWithOperations,
  gvr: GroupVersionResource,
  operation: Operation,
  requestName?: string,
  clusterScoped?: boolean,
): boolean {
  if (rule.operations && rule.operations.length > 0) {
    const opMatch = rule.operations.some(
      (op) => op === '*' || (op as string) === operation,
    );
    if (!opMatch) return false;
  }

  if (!listPatternMatch(rule.apiGroups, gvr.apiGroup)) return false;
  if (!listPatternMatch(rule.apiVersions, gvr.apiVersion)) return false;

  const sub = gvr.subresource ?? '';
  const resourceDesignator = sub ? `${gvr.resource}/${sub}` : gvr.resource;
  if (!listPatternMatch(rule.resources, resourceDesignator)) return false;

  if (rule.resourceNames && rule.resourceNames.length > 0) {
    if (!requestName || !rule.resourceNames.includes(requestName)) return false;
  }

  if (rule.scope === 'Cluster' && clusterScoped !== true) return false;
  if (rule.scope === 'Namespaced' && clusterScoped === true) return false;

  return true;
}

function anyRuleMatches(
  rules: NamedRuleWithOperations[] | undefined,
  gvr: GroupVersionResource,
  operation: Operation,
  requestName?: string,
  clusterScoped?: boolean,
): boolean {
  if (!rules || rules.length === 0) return false;
  return rules.some((rule) =>
    ruleMatchesResource(rule, gvr, operation, requestName, clusterScoped),
  );
}

/**
 * Full matchConstraints evaluation. Exclusions take precedence over inclusions:
 * any matching excludeResourceRules rule vetoes the request even if a resourceRule matches.
 */
export function matchResources(
  constraints: MatchResources | undefined,
  request: AdmissionRequest,
): boolean {
  if (!constraints) return true;

  if (
    constraints.excludeResourceRules &&
    anyRuleMatches(
      constraints.excludeResourceRules,
      request.resource,
      request.operation,
      request.name,
      request.clusterScoped,
    )
  ) {
    return false;
  }

  const included = constraints.resourceRules;
  if (included && included.length > 0) {
    if (
      !anyRuleMatches(
        included,
        request.resource,
        request.operation,
        request.name,
        request.clusterScoped,
      )
    ) {
      return false;
    }
  }

  if (!labelSelectorMatches(constraints.namespaceSelector, namespaceLabels(request))) {
    return false;
  }

  const objectLabels = objectLabelsForSelector(request);
  if (!labelSelectorMatches(constraints.objectSelector, objectLabels)) {
    return false;
  }

  return true;
}

function namespaceLabels(request: AdmissionRequest): Record<string, string> | undefined {
  if (request.clusterScoped === true) {
    // Cluster-scoped resources have no owning namespace; namespaceSelector cannot constrain them.
    return undefined;
  }
  return request.namespaceObject?.metadata?.labels;
}

function objectLabelsForSelector(request: AdmissionRequest): Record<string, string> | undefined {
  // On DELETE the incoming object may be absent; oldObject carries its labels.
  const target = request.object ?? request.oldObject;
  const labels = (target as { metadata?: { labels?: Record<string, string> } } | undefined)
    ?.metadata?.labels;
  return labels as Record<string, string> | undefined;
}

export function labelSelectorMatches(
  selector: LabelSelector | undefined,
  labels: Record<string, string> | undefined,
): boolean {
  if (!selector) return true;
  // No label context (e.g. namespaceSelector against a cluster-scoped resource)
  // means the selector cannot constrain the request.
  if (labels === undefined) return true;
  const effectiveLabels = labels ?? {};

  if (selector.matchLabels) {
    for (const [key, value] of Object.entries(selector.matchLabels)) {
      if (effectiveLabels[key] !== value) return false;
    }
  }

  if (selector.matchExpressions) {
    for (const requirement of selector.matchExpressions) {
      if (!requirementMatches(requirement, effectiveLabels)) return false;
    }
  }

  return true;
}

function requirementMatches(
  requirement: LabelSelectorRequirement,
  labels: Record<string, string>,
): boolean {
  const hasKey = Object.prototype.hasOwnProperty.call(labels, requirement.key);
  switch (requirement.operator) {
    case 'In':
      return hasKey && (requirement.values ?? []).includes(labels[requirement.key]);
    case 'NotIn':
      return !hasKey || !(requirement.values ?? []).includes(labels[requirement.key]);
    case 'Exists':
      return hasKey;
    case 'DoesNotExist':
      return !hasKey;
    default:
      return false;
  }
}
