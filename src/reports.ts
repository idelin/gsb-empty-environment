import { encodeReportWithDictionary } from './dictionary.ts';
import { canonicalJson, deepFreeze, stableId } from './fingerprint.ts';
import {
  analyzeEvidence,
  type AuthorizedContribution,
  type EvidenceAnalysis,
} from './projection.ts';
import { isScopeMatch, verifySnapshotIntegrity } from './snapshot.ts';
import type {
  CandidateReport,
  CompressionDictionary,
  EvidenceSnapshot,
  MissingEvidenceDescription,
  RecipientContext,
  VerificationResult,
  WireBucket,
  WireReport,
} from './types.ts';

const ANONYMOUS_BUCKET = '\u0000anonymous';

export interface BuildReportPlanOptions {
  readonly recipient?: RecipientContext;
  readonly dictionary?: CompressionDictionary;
  readonly defaultMaxContributions?: number;
}

export interface ReportPlan {
  readonly candidates: readonly CandidateReport[];
  readonly analysis: EvidenceAnalysis;
}

interface ScopeGroup {
  readonly key: string;
  readonly scope: Record<string, string>;
  readonly maxContributions: number;
  readonly contributions: AuthorizedContribution[];
}

interface BucketBuilder {
  readonly sortKey: string;
  readonly bucketKey?: string;
  readonly countIds: Set<string>;
  earliestValue?: number;
  readonly earliestIds: Set<string>;
  readonly statusCodeIds: Map<number, Set<string>>;
  readonly presenceIds: Set<string>;
}

export function buildReportPlan(
  snapshot: EvidenceSnapshot,
  options: BuildReportPlanOptions = {},
): ReportPlan {
  const analysis = analyzeEvidence(snapshot, options.recipient);
  const defaultMax = Math.max(1, Math.floor(options.defaultMaxContributions ?? 10));
  const groups = new Map<string, ScopeGroup>();

  for (const contribution of analysis.contributions) {
    const event = contribution.event;
    const scope = {
      tenantId: event.tenantId,
      principalId: event.principalId,
      endpointGroupId: event.endpoint.groupId,
      endpointId: event.endpoint.endpointId,
      endpointUrl: event.endpoint.url,
      topLevelSite: event.topLevelSite,
      policySource: event.policySourceId,
      documentGeneration: event.documentGeneration,
    };
    const key = canonicalJson(scope);
    let group = groups.get(key);
    if (!group) {
      group = {
        key,
        scope,
        maxContributions: contribution.policy.batchMaxContributions ?? defaultMax,
        contributions: [],
      };
      groups.set(key, group);
    }
    group.contributions.push(contribution);
  }

  const candidates: CandidateReport[] = [];
  for (const group of groups.values()) {
    const max = Math.max(1, Math.floor(group.maxContributions));
    for (let start = 0, batchIndex = 0; start < group.contributions.length; start += max, batchIndex += 1) {
      const chunk = group.contributions.slice(start, start + max);
      candidates.push(
        buildCandidate({
          snapshot,
          group,
          chunk,
          batchIndex,
          startOrdinal: start,
          maxContributions: max,
          dictionary: options.dictionary,
        }),
      );
    }
  }

  return { candidates: deepFreeze(candidates), analysis };
}

interface BuildCandidateInput {
  readonly snapshot: EvidenceSnapshot;
  readonly group: ScopeGroup;
  readonly chunk: readonly AuthorizedContribution[];
  readonly batchIndex: number;
  readonly startOrdinal: number;
  readonly maxContributions: number;
  readonly dictionary?: CompressionDictionary;
}

function buildCandidate(input: BuildCandidateInput): CandidateReport {
  const { snapshot, group, chunk, batchIndex, startOrdinal, maxContributions, dictionary } = input;
  const builders = new Map<string, BucketBuilder>();

  for (const contribution of chunk) {
    const event = contribution.event;
    const fields = contribution.effectiveFields;
    const allowsPresence = fields.has('bucketPresence');
    const sortKey = allowsPresence ? `named:${event.bucketKey}` : ANONYMOUS_BUCKET;
    let builder = builders.get(sortKey);
    if (!builder) {
      builder = {
        sortKey,
        bucketKey: allowsPresence ? event.bucketKey : undefined,
        countIds: new Set(),
        earliestIds: new Set(),
        statusCodeIds: new Map(),
        presenceIds: new Set(),
      };
      builders.set(sortKey, builder);
    }

    if (allowsPresence) {
      builder.presenceIds.add(event.id);
    }
    if (fields.has('count')) {
      builder.countIds.add(event.id);
    }
    if (fields.has('earliestTimestamp')) {
      if (builder.earliestValue === undefined || event.occurredAt < builder.earliestValue) {
        builder.earliestValue = event.occurredAt;
        builder.earliestIds.clear();
        builder.earliestIds.add(event.id);
      } else if (event.occurredAt === builder.earliestValue) {
        builder.earliestIds.add(event.id);
      }
    }
    if (fields.has('statusCodes')) {
      const ids = builder.statusCodeIds.get(event.statusCode) ?? new Set<string>();
      ids.add(event.id);
      builder.statusCodeIds.set(event.statusCode, ids);
    }
  }

  const buckets: WireBucket[] = [...builders.values()]
    .sort((left, right) => compareBucketSortKey(left.sortKey, right.sortKey))
    .map((builder) => {
      const bucket: WireBucket = {};
      if (builder.bucketKey !== undefined) {
        (bucket as { bucketKey?: string }).bucketKey = builder.bucketKey;
        (bucket as { present?: true }).present = true;
      }
      if (builder.countIds.size > 0) {
        (bucket as { count?: number }).count = builder.countIds.size;
      }
      if (builder.earliestValue !== undefined && builder.earliestIds.size > 0) {
        (bucket as { earliestTimestamp?: number }).earliestTimestamp = builder.earliestValue;
      }
      if (builder.statusCodeIds.size > 0) {
        (bucket as { statusCodes?: number[] }).statusCodes = [
          ...builder.statusCodeIds.keys(),
        ].sort((left, right) => left - right);
      }
      return deepFreeze(bucket);
    });

  const contributorIds = chunk.map((contribution) => contribution.event.id);
  const allAllow = (field: string): boolean =>
    chunk.every((contribution) => contribution.effectiveFields.has(field));

  const batchId = stableId('batch', {
    scope: group.scope,
    batchIndex,
    contributorIds,
    maxContributions,
  });

  const wireWithoutReportId = {
    schema: 'gsb.aggregate-report/v1' as const,
    batchId,
    batchIndex,
    tenantId: group.scope.tenantId,
    principalId: group.scope.principalId,
    endpointGroupId: group.scope.endpointGroupId,
    endpointId: group.scope.endpointId,
    ...(allAllow('topLevelSite') ? { topLevelSite: group.scope.topLevelSite } : {}),
    ...(allAllow('policySource') ? { policySource: group.scope.policySource } : {}),
    ...(allAllow('documentGeneration')
      ? { documentGeneration: group.scope.documentGeneration }
      : {}),
    ...(dictionary ? { dictionaryId: dictionary.id } : {}),
    buckets,
  };
  const reportId = stableId('rpt', { ...wireWithoutReportId, reportIdSeed: 'wire' });
  const wire: WireReport = deepFreeze({ ...wireWithoutReportId, reportId });
  const encoded = dictionary
    ? deepFreeze(encodeReportWithDictionary(wire, dictionary))
    : undefined;

  const countEventIds = [
    ...new Set(
      [...builders.values()].flatMap((builder) => [...builder.countIds]),
    ),
  ].sort();
  const earliestEventIds = [
    ...new Set(
      [...builders.values()].flatMap((builder) => [...builder.earliestIds]),
    ),
  ].sort();
  const bucketPresenceEventIds = [
    ...new Set(
      [...builders.values()].flatMap((builder) => [...builder.presenceIds]),
    ),
  ].sort();
  const statusCodeEventIds: Record<string, readonly string[]> = {};
  for (const builder of builders.values()) {
    for (const [statusCode, ids] of builder.statusCodeIds) {
      statusCodeEventIds[statusCode] = [...ids].sort();
    }
  }

  const proof = deepFreeze({
    schema: 'gsb.report-proof/v1' as const,
    reportHash: fingerprintWire(wire),
    authorizedEvidenceHash: stableId('evidence', chunk.map((item) => item.event)),
    snapshotHash: snapshot.snapshotHash,
    policyIds: [...new Set(chunk.map((item) => item.policy.policyId))].sort(),
    contributorIds: deepFreeze([...contributorIds]),
    countEventIds: deepFreeze(countEventIds),
    earliestEventIds: deepFreeze(earliestEventIds),
    statusCodeEventIds: deepFreeze(statusCodeEventIds),
    bucketPresenceEventIds: deepFreeze(bucketPresenceEventIds),
    batch: deepFreeze({
      index: batchIndex,
      startOrdinal,
      endOrdinal: startOrdinal + chunk.length,
      maxContributions,
      state: chunk.length === maxContributions ? ('sealed' as const) : ('open' as const),
    }),
  });

  return deepFreeze({
    ...wire,
    endpointUrl: group.scope.endpointUrl,
    state: chunk.length === maxContributions ? ('sealed' as const) : ('open' as const),
    proof,
    ...(encoded ? { encoded } : {}),
  });
}

function compareBucketSortKey(left: string, right: string): number {
  if (left === ANONYMOUS_BUCKET && right !== ANONYMOUS_BUCKET) {
    return -1;
  }
  if (right === ANONYMOUS_BUCKET && left !== ANONYMOUS_BUCKET) {
    return 1;
  }
  return left < right ? -1 : left > right ? 1 : 0;
}

function fingerprintWire(wire: WireReport): string {
  return stableId('wire', wire);
}

export function toWireReport(candidate: CandidateReport): WireReport {
  return {
    schema: candidate.schema,
    reportId: candidate.reportId,
    batchId: candidate.batchId,
    batchIndex: candidate.batchIndex,
    tenantId: candidate.tenantId,
    principalId: candidate.principalId,
    endpointGroupId: candidate.endpointGroupId,
    endpointId: candidate.endpointId,
    ...(candidate.topLevelSite !== undefined
      ? { topLevelSite: candidate.topLevelSite }
      : {}),
    ...(candidate.policySource !== undefined ? { policySource: candidate.policySource } : {}),
    ...(candidate.documentGeneration !== undefined
      ? { documentGeneration: candidate.documentGeneration }
      : {}),
    ...(candidate.dictionaryId !== undefined
      ? { dictionaryId: candidate.dictionaryId }
      : {}),
    buckets: candidate.buckets,
  };
}

export interface VerifyReportOptions {
  readonly report: CandidateReport | WireReport;
  readonly snapshot: EvidenceSnapshot;
  readonly recipient?: RecipientContext;
  readonly dictionary?: CompressionDictionary;
}

export function verifyReportForDelivery(
  options: VerifyReportOptions,
): VerificationResult {
  const { snapshot } = options;
  if (!verifySnapshotIntegrity(snapshot)) {
    return {
      status: 'rejected',
      snapshotHash: snapshot.snapshotHash,
      violations: ['evidence snapshot is not an immutable, intact snapshot'],
    };
  }

  const candidateLike = options.report as Partial<CandidateReport>;
  const hasProof = candidateLike.proof !== undefined;
  const wire = hasProof
    ? toWireReport(options.report as CandidateReport)
    : deepFreeze(options.report as WireReport);

  const baseScope = {
    tenantId: wire.tenantId,
    principalId: wire.principalId,
    topLevelSite: wire.topLevelSite,
    endpointGroupId: wire.endpointGroupId,
    documentGeneration: wire.documentGeneration,
  };

  const missingPages = snapshot.knownMissingPages.filter(
    (page) => {
      if (page.scope === undefined) {
        return true;
      }
      const reportLevelScope = { ...page.scope, bucketKey: undefined };
      return isScopeMatch(baseScope, reportLevelScope);
    },
  );
  if (missingPages.length > 0) {
    return {
      status: 'pending_attestation',
      snapshotHash: snapshot.snapshotHash,
      missing: missingPages.map((page) => ({
        kind: 'missing_page' as const,
        reference: page.pageId,
        detail: page.reason ?? 'known event page is absent',
      })),
    };
  }

  const recipient = options.recipient ?? {
    tenantId: wire.tenantId,
    principalId: wire.principalId,
    endpointId: wire.endpointId,
    endpointGroupId: wire.endpointGroupId,
  };
  const plan = buildReportPlan(snapshot, {
    recipient,
    dictionary: options.dictionary,
  });

  const relevantPending = plan.analysis.pending.filter((item) =>
    isPendingRelevant(item.eventId, item.missingEventIds, snapshot, wire, candidateLike),
  );
  if (relevantPending.length > 0) {
    const missing: MissingEvidenceDescription[] = relevantPending.flatMap((item) => {
      const descriptions: MissingEvidenceDescription[] = item.missingEventIds.map((id) => ({
        kind: 'missing_causal_event' as const,
        reference: id,
        detail: `missing causal prefix for ${item.eventId}`,
      }));
      if (descriptions.length === 0) {
        descriptions.push({
          kind: 'missing_causal_event',
          reference: item.eventId,
          detail: item.reason,
        });
      }
      return descriptions;
    });
    return {
      status: 'pending_attestation',
      snapshotHash: snapshot.snapshotHash,
      missing,
    };
  }

  const expected =
    plan.candidates.find((candidate) => candidate.reportId === wire.reportId) ??
    plan.candidates.find((candidate) => candidate.batchId === wire.batchId) ??
    plan.candidates.find(
      (candidate) =>
        candidate.batchIndex === wire.batchIndex &&
        candidate.tenantId === wire.tenantId &&
        candidate.principalId === wire.principalId &&
        candidate.endpointGroupId === wire.endpointGroupId &&
        candidate.documentGeneration === wire.documentGeneration,
    );

  if (!expected) {
    return {
      status: 'rejected',
      snapshotHash: snapshot.snapshotHash,
      violations: ['report is not reproducible from currently authorized evidence'],
    };
  }

  const violations: string[] = [];
  const expectedWire = toWireReport(expected);
  if (canonicalJson(wire) !== canonicalJson(expectedWire)) {
    violations.push('one or more report fields are not supported by the evidence snapshot');
  }
  if (hasProof) {
    const candidate = options.report as CandidateReport;
    if (candidate.proof.snapshotHash !== snapshot.snapshotHash) {
      violations.push('proof is bound to a different evidence snapshot');
    }
    if (canonicalJson(candidate.proof) !== canonicalJson(expected.proof)) {
      violations.push('report provenance or batch attribution does not recompute');
    }
    if (candidate.endpointUrl !== expected.endpointUrl) {
      violations.push('endpoint URL does not match authorized binding');
    }
    if (candidate.state !== expected.state) {
      violations.push('batch seal state does not match evidence');
    }
    if (candidate.encoded && expected.encoded) {
      if (canonicalJson(candidate.encoded) !== canonicalJson(expected.encoded)) {
        violations.push('dictionary encoding does not match canonical report');
      }
    }
  }

  if (options.dictionary) {
    if (wire.dictionaryId !== options.dictionary.id) {
      violations.push('report dictionary id does not match verification dictionary');
    }
    if (!expected.encoded) {
      violations.push('dictionary encoding could not be recomputed');
    }
  }

  if (violations.length > 0) {
    return { status: 'rejected', snapshotHash: snapshot.snapshotHash, violations };
  }

  return {
    status: 'verified',
    report: expected,
    wire: expectedWire,
    payload: canonicalJson(expectedWire),
    contentType: 'application/json',
  };
}

export const verifyReportBeforeDelivery = verifyReportForDelivery;
export const verifyImmutableReport = verifyReportForDelivery;
export const verifyReport = verifyReportForDelivery;

function isPendingRelevant(
  pendingEventId: string,
  missingEventIds: readonly string[],
  snapshot: EvidenceSnapshot,
  wire: WireReport,
  candidate: Partial<CandidateReport>,
): boolean {
  if (candidate.proof?.contributorIds.includes(pendingEventId)) {
    return true;
  }
  if (
    missingEventIds.some((id) => candidate.proof?.contributorIds.includes(id) ?? false)
  ) {
    return true;
  }
  const event = snapshot.byId[pendingEventId];
  if (!event) {
    return true;
  }
  const eventScope = scopeFromEvent(event);
  const reportLevelEventScope = { ...eventScope, bucketKey: undefined };
  return isScopeMatch(
    {
      tenantId: wire.tenantId,
      principalId: wire.principalId,
      endpointGroupId: wire.endpointGroupId,
      documentGeneration: wire.documentGeneration,
    },
    reportLevelEventScope,
  );
}

function scopeFromEvent(event: EvidenceSnapshot['events'][number]): Parameters<typeof isScopeMatch>[1] {
  if (event.kind === 'policy-replaced') {
    return {
      tenantId: event.tenantId,
      principalId: event.policy.principalId,
    };
  }
  if (event.kind === 'navigation') {
    return {
      tenantId: event.tenantId,
      principalId: event.principalId,
      topLevelSite: event.topLevelSite,
      documentGeneration: event.fromGeneration ?? event.toGeneration,
    };
  }
  if (event.kind === 'subdomain-inheritance') {
    return { tenantId: event.tenantId, principalId: event.principalId };
  }
  if (event.kind === 'endpoint-group-revoked') {
    return {
      tenantId: event.tenantId,
      principalId: event.principalId,
      endpointGroupId: event.endpointGroupId,
    };
  }
  if (event.kind === 'report-permission-revoked') {
    return {
      tenantId: event.tenantId,
      principalId: event.principalId,
      endpointGroupId: event.endpointGroupId,
      documentGeneration: event.documentGeneration,
    };
  }
  return {
    tenantId: event.tenantId,
    principalId: event.principalId,
    topLevelSite: event.topLevelSite,
    endpointGroupId: event.endpoint.groupId,
    documentGeneration: event.documentGeneration,
    bucketKey: event.bucketKey,
  };
}
