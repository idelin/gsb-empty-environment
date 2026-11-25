import { verifySamplingCommitment } from './sampling.ts';
import type {
  AuthorizationReason,
  ControlEvidence,
  DeniedContribution,
  EvidenceEvent,
  EvidenceSnapshot,
  FailureEvidence,
  NavigationEvent,
  PendingEvidence,
  PolicySpec,
  RecipientContext,
  ReportPermissionRevokedEvent,
  SubdomainInheritanceEvent,
} from './types.ts';

export interface CausalAnalysis {
  readonly ordered: readonly EvidenceEvent[];
  readonly readyIds: ReadonlySet<string>;
  readonly pending: readonly PendingEvidence[];
}

export interface PolicyProjection {
  readonly causal: CausalAnalysis;
  readonly policiesById: ReadonlyMap<string, PolicySpec>;
  readonly currentPolicyIdByLineage: ReadonlyMap<string, string>;
  readonly inheritance: readonly SubdomainInheritanceEvent[];
  readonly navigations: readonly NavigationEvent[];
  readonly permissionRevocations: readonly ReportPermissionRevokedEvent[];
  readonly revokedEndpointGroups: ReadonlySet<string>;
}

export interface AuthorizedContribution {
  readonly event: FailureEvidence;
  readonly policy: PolicySpec;
  readonly effectiveFields: ReadonlySet<string>;
  readonly endpoint: FailureEvidence['endpoint'];
}

export interface EvidenceAnalysis {
  readonly projection: PolicyProjection;
  readonly contributions: readonly AuthorizedContribution[];
  readonly denied: readonly DeniedContribution[];
  readonly pending: readonly PendingEvidence[];
}

const CONTROL_KINDS = new Set([
  'policy-replaced',
  'navigation',
  'subdomain-inheritance',
  'endpoint-group-revoked',
  'report-permission-revoked',
]);

export function isControlEvent(event: EvidenceEvent): event is ControlEvidence {
  return CONTROL_KINDS.has(event.kind);
}

export function causalOrder(events: readonly EvidenceEvent[]): CausalAnalysis {
  const byId = new Map(events.map((event) => [event.id, event]));
  const dependencies = new Map<string, readonly string[]>();
  const missingDirect = new Map<string, string[]>();

  for (const event of events) {
    const prefix = event.causalPrefix ?? [];
    dependencies.set(event.id, prefix);
    const missing = prefix.filter((id) => !byId.has(id));
    if (missing.length > 0) {
      missingDirect.set(event.id, missing);
    }
  }

  const indegree = new Map<string, number>();
  const dependents = new Map<string, string[]>();
  for (const event of events) {
    if (missingDirect.has(event.id)) {
      continue;
    }
    const deps = dependencies.get(event.id) ?? [];
    indegree.set(event.id, deps.length);
    for (const dependencyId of deps) {
      const list = dependents.get(dependencyId) ?? [];
      list.push(event.id);
      dependents.set(dependencyId, list);
    }
  }

  const comparable = (id: string): number => {
    const event = byId.get(id);
    return event?.ingestSequence ?? Number.MAX_SAFE_INTEGER;
  };
  const compare = (left: string, right: string): number => {
    const leftEvent = byId.get(left);
    const rightEvent = byId.get(right);
    const timeDelta = (leftEvent?.occurredAt ?? 0) - (rightEvent?.occurredAt ?? 0);
    if (timeDelta !== 0) {
      return timeDelta;
    }
    const ingestDelta = comparable(left) - comparable(right);
    if (ingestDelta !== 0) {
      return ingestDelta;
    }
    return left < right ? -1 : left > right ? 1 : 0;
  };

  const ready: string[] = [];
  for (const [id, degree] of indegree) {
    if (degree === 0) {
      ready.push(id);
    }
  }
  ready.sort(compare);

  const ordered: EvidenceEvent[] = [];
  const processed = new Set<string>();
  while (ready.length > 0) {
    const id = ready.shift()!;
    if (processed.has(id)) {
      continue;
    }
    processed.add(id);
    const event = byId.get(id);
    if (event) {
      ordered.push(event);
    }
    for (const dependentId of dependents.get(id) ?? []) {
      const degree = (indegree.get(dependentId) ?? 1) - 1;
      indegree.set(dependentId, degree);
      if (degree === 0) {
        ready.push(dependentId);
        ready.sort(compare);
      }
    }
  }

  const pending: PendingEvidence[] = [];
  for (const event of events) {
    if (processed.has(event.id)) {
      continue;
    }
    const directMissing = missingDirect.get(event.id);
    if (directMissing) {
      pending.push({
        eventId: event.id,
        missingEventIds: directMissing,
        reason: 'missing_causal_prefix',
      });
      continue;
    }
    const reachableMissing = collectReachableMissing(event.id, dependencies, new Set());
    if (reachableMissing.length > 0) {
      pending.push({
        eventId: event.id,
        missingEventIds: reachableMissing,
        reason: 'missing_causal_prefix',
      });
    } else {
      pending.push({
        eventId: event.id,
        missingEventIds: [],
        reason: 'causal_cycle',
      });
    }
  }

  return { ordered, readyIds: processed, pending };
}

function collectReachableMissing(
  eventId: string,
  dependencies: ReadonlyMap<string, readonly string[]>,
  seen: Set<string>,
): string[] {
  if (seen.has(eventId)) {
    return [];
  }
  seen.add(eventId);
  const result: string[] = [];
  for (const dependencyId of dependencies.get(eventId) ?? []) {
    if (!dependencies.has(dependencyId)) {
      result.push(dependencyId);
    } else {
      result.push(...collectReachableMissing(dependencyId, dependencies, seen));
    }
  }
  return [...new Set(result)];
}

export function buildPolicyProjection(snapshot: EvidenceSnapshot): PolicyProjection {
  const causal = causalOrder(snapshot.events);
  const policiesById = new Map<string, PolicySpec>();
  const currentPolicyIdByLineage = new Map<string, string>();
  const inheritance: SubdomainInheritanceEvent[] = [];
  const navigations: NavigationEvent[] = [];
  const permissionRevocations: ReportPermissionRevokedEvent[] = [];
  const revokedEndpointGroups = new Set<string>();

  for (const event of causal.ordered) {
    if (event.kind === 'policy-replaced') {
      policiesById.set(event.policy.policyId, event.policy);
      currentPolicyIdByLineage.set(event.policy.lineageId, event.policy.policyId);
    } else if (event.kind === 'subdomain-inheritance') {
      inheritance.push(event);
    } else if (event.kind === 'navigation') {
      navigations.push(event);
    } else if (event.kind === 'report-permission-revoked') {
      permissionRevocations.push(event);
    } else if (event.kind === 'endpoint-group-revoked') {
      revokedEndpointGroups.add(endpointGroupKey(event.tenantId, event.principalId, event.endpointGroupId));
    }
  }

  return {
    causal,
    policiesById,
    currentPolicyIdByLineage,
    inheritance,
    navigations,
    permissionRevocations,
    revokedEndpointGroups,
  };
}

export function analyzeEvidence(
  snapshot: EvidenceSnapshot,
  recipient?: RecipientContext,
): EvidenceAnalysis {
  const projection = buildPolicyProjection(snapshot);
  const pending = [...projection.causal.pending];
  const pendingIds = new Set(pending.map((item) => item.eventId));
  const contributions: AuthorizedContribution[] = [];
  const denied: DeniedContribution[] = [];

  for (const event of snapshot.events) {
    if (event.kind !== 'failure') {
      continue;
    }
    if (pendingIds.has(event.id)) {
      continue;
    }
    const authorization = authorizeFailure(event, projection, recipient);
    if (authorization.ok) {
      contributions.push({
        event,
        policy: authorization.policy,
        effectiveFields: authorization.fields,
        endpoint: authorization.endpoint,
      });
    } else {
      denied.push({ eventId: event.id, reason: authorization.reason });
    }
  }

  return { projection, contributions, denied, pending };
}

type AuthorizationOutcome =
  | {
      readonly ok: true;
      readonly policy: PolicySpec;
      readonly fields: ReadonlySet<string>;
      readonly endpoint: FailureEvidence['endpoint'];
    }
  | { readonly ok: false; readonly reason: AuthorizationReason };

export function authorizeFailure(
  event: FailureEvidence,
  projection: PolicyProjection,
  recipient?: RecipientContext,
): AuthorizationOutcome {
  if (!verifySamplingCommitment(event.id, event.sampling)) {
    return { ok: false, reason: 'invalid_sampling_commitment' };
  }
  if (!event.sampling.included) {
    return { ok: false, reason: 'sampling_excluded' };
  }

  const exactPolicy = projection.policiesById.get(event.policySourceId);
  let policy = exactPolicy;
  if (policy === undefined && event.lineageId) {
    const currentId = projection.currentPolicyIdByLineage.get(event.lineageId);
    policy = currentId ? projection.policiesById.get(currentId) : undefined;
  } else if (policy) {
    const currentId = projection.currentPolicyIdByLineage.get(policy.lineageId);
    const currentPolicy = currentId
      ? projection.policiesById.get(currentId)
      : undefined;
    if (currentPolicy) {
      policy = currentPolicy;
    }
  }
  if (!policy) {
    return { ok: false, reason: 'unknown_policy' };
  }
  if (event.tenantId !== policy.tenantId) {
    return { ok: false, reason: 'tenant_mismatch' };
  }
  if (event.principalId !== policy.principalId) {
    return { ok: false, reason: 'principal_mismatch' };
  }
  if (!policy.enabled) {
    return { ok: false, reason: 'policy_disabled' };
  }

  if (
    policy.topLevelSites &&
    policy.topLevelSites.length > 0 &&
    !policy.topLevelSites.includes(event.topLevelSite)
  ) {
    return { ok: false, reason: 'top_level_site_not_in_scope' };
  }
  if (!isDocumentHostAllowed(event, policy, projection)) {
    return { ok: false, reason: 'subdomain_not_in_scope' };
  }
  if (isGenerationRevoked(event, projection)) {
    return { ok: false, reason: 'document_generation_revoked' };
  }
  if (
    projection.revokedEndpointGroups.has(
      endpointGroupKey(event.tenantId, event.principalId, event.endpoint.groupId),
    )
  ) {
    return { ok: false, reason: 'endpoint_group_revoked' };
  }

  let endpoint = event.endpoint;
  if (policy.endpoints && policy.endpoints.length > 0) {
    const allowedEndpoint = policy.endpoints.find(
      (candidate) =>
        candidate.endpointId === event.endpoint.endpointId &&
        candidate.groupId === event.endpoint.groupId,
    );
    if (!allowedEndpoint || allowedEndpoint.url !== event.endpoint.url) {
      return { ok: false, reason: 'endpoint_not_allowed' };
    }
    endpoint = allowedEndpoint;
  }

  if (recipient?.tenantId && recipient.tenantId !== event.tenantId) {
    return { ok: false, reason: 'recipient_mismatch' };
  }
  if (recipient?.principalId && recipient.principalId !== event.principalId) {
    return { ok: false, reason: 'recipient_mismatch' };
  }
  if (recipient?.endpointId && recipient.endpointId !== endpoint.endpointId) {
    return { ok: false, reason: 'recipient_mismatch' };
  }
  if (recipient?.endpointGroupId && recipient.endpointGroupId !== endpoint.groupId) {
    return { ok: false, reason: 'recipient_mismatch' };
  }

  const fields = new Set(
    event.allowedFields.filter((field) => policy!.allowedFields.includes(field)),
  );
  if (!fields.has('batchBoundary')) {
    return { ok: false, reason: 'batch_boundary_not_allowed' };
  }
  const hasOutputField =
    fields.has('count') ||
    fields.has('earliestTimestamp') ||
    fields.has('statusCodes') ||
    fields.has('bucketPresence');
  if (!hasOutputField || fields.size === 0) {
    return { ok: false, reason: 'no_disclosable_fields' };
  }

  return { ok: true, policy, fields, endpoint };
}

export function endpointGroupKey(
  tenantId: string,
  principalId: string,
  groupId: string,
): string {
  return `${tenantId}\u0000${principalId}\u0000${groupId}`;
}

function isSubdomain(host: string, parentHost: string): boolean {
  return host !== parentHost && host.endsWith(`.${parentHost}`);
}

function isDocumentHostAllowed(
  event: FailureEvidence,
  policy: PolicySpec,
  projection: PolicyProjection,
): boolean {
  if (!policy.sourceHost) {
    return true;
  }
  const host = event.documentHost;
  if (!host) {
    return false;
  }
  if (host === policy.sourceHost) {
    return true;
  }
  if (policy.includeSubdomains && isSubdomain(host, policy.sourceHost)) {
    return true;
  }
  for (const inheritance of projection.inheritance) {
    if (!inheritance.enabled || inheritance.principalId !== event.principalId) {
      continue;
    }
    if (inheritance.policyId && inheritance.policyId !== policy.policyId) {
      continue;
    }
    if (inheritance.childHost) {
      if (
        host === inheritance.childHost ||
        isSubdomain(host, inheritance.childHost) ||
        host === inheritance.parentHost ||
        isSubdomain(host, inheritance.parentHost)
      ) {
        return true;
      }
    } else if (
      host === inheritance.parentHost ||
      isSubdomain(host, inheritance.parentHost)
    ) {
      return true;
    }
  }
  return false;
}

function isGenerationRevoked(
  event: FailureEvidence,
  projection: PolicyProjection,
): boolean {
  for (const revocation of projection.permissionRevocations) {
    if (revocation.principalId !== event.principalId) {
      continue;
    }
    if (
      revocation.documentGeneration &&
      revocation.documentGeneration !== event.documentGeneration
    ) {
      continue;
    }
    if (
      revocation.endpointGroupId &&
      revocation.endpointGroupId !== event.endpoint.groupId
    ) {
      continue;
    }
    return true;
  }

  for (const navigation of projection.navigations) {
    if (
      navigation.principalId !== event.principalId ||
      navigation.revokePreviousReportPermission !== true
    ) {
      continue;
    }
    if (navigation.fromGeneration) {
      if (event.documentGeneration === navigation.fromGeneration) {
        return true;
      }
    } else if (event.documentGeneration !== navigation.toGeneration) {
      return true;
    }
  }
  return false;
}
