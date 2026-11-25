import { canonicalJson, fingerprint, stableId } from './fingerprint.ts';
import { authorizeFailure, buildPolicyProjection } from './projection.ts';
import { verifyReportForDelivery } from './reports.ts';
import { isScopeMatch } from './snapshot.ts';
import type {
  CandidateReport,
  EndpointRef,
  EvidenceSnapshot,
  RecipientContext,
  VerificationResult,
  WireReport,
} from './types.ts';

export type DeliveryStatus = 'approved' | 'held' | 'in_flight' | 'acknowledged' | 'suppressed';

export interface DeliveryRecord {
  readonly deliveryId: string;
  readonly reportId: string;
  readonly endpoint: EndpointRef;
  readonly wire: WireReport;
  readonly payload: string;
  readonly payloadHash: string;
  readonly contentType: 'application/json' | 'application/gsb.dict-ref+json';
  readonly status: DeliveryStatus;
  readonly attempts: number;
  readonly revocationVisible: boolean;
  readonly approvedSnapshotHash: string;
  readonly approvedAt: number;
  readonly acknowledgedAt?: number;
  readonly blockReason?: string;
}

export interface MutableDeliveryRecord extends DeliveryRecord {
  status: DeliveryStatus;
  attempts: number;
  revocationVisible: boolean;
  acknowledgedAt?: number;
  blockReason?: string;
  latestCheck?: 'approved' | 'pending' | 'rejected';
  readonly approvedSnapshot: EvidenceSnapshot;
  readonly candidate: CandidateReport;
  readonly useDictionaryPayload: boolean;
}

export interface ApproveDeliveryOptions {
  readonly candidate: CandidateReport;
  readonly snapshot: EvidenceSnapshot;
  readonly dictionary?: Parameters<typeof verifyReportForDelivery>[0]['dictionary'];
  readonly recipient?: RecipientContext;
  readonly now?: number;
  readonly deliveryId?: string;
  readonly useDictionaryPayload?: boolean;
}

export type ApproveDeliveryResult =
  | { readonly ok: true; readonly delivery: DeliveryRecord }
  | { readonly ok: false; readonly verification: VerificationResult };

export type BeginAttemptResult =
  | {
      readonly ok: true;
      readonly deliveryId: string;
      readonly attempt: number;
      readonly payload: string;
      readonly payloadHash: string;
      readonly contentType: DeliveryRecord['contentType'];
      readonly endpoint: EndpointRef;
    }
  | {
      readonly ok: false;
      readonly deliveryId?: string;
      readonly status: DeliveryStatus | 'unknown';
      readonly reason: string;
    };

export class DeliveryOutbox {
  private readonly records = new Map<string, MutableDeliveryRecord>();
  private latestSnapshot: EvidenceSnapshot | null = null;

  approve(options: ApproveDeliveryOptions): ApproveDeliveryResult {
    const verification = verifyReportForDelivery({
      report: options.candidate,
      snapshot: options.snapshot,
      dictionary: options.dictionary,
      recipient: options.recipient,
    });
    if (verification.status !== 'verified') {
      return { ok: false, verification };
    }

    const useDictionaryPayload =
      options.useDictionaryPayload === true && options.candidate.encoded !== undefined;
    const payload = useDictionaryPayload
      ? options.candidate.encoded!.payload
      : verification.payload;
    const contentType = useDictionaryPayload
      ? 'application/gsb.dict-ref+json'
      : 'application/json';
    const payloadHash = fingerprint(payload);
    const deliveryId =
      options.deliveryId ??
      stableId('dlv', {
        reportId: verification.wire.reportId,
        payloadHash,
        endpoint: options.candidate.endpointUrl,
        endpointId: verification.wire.endpointId,
      });

    const record: MutableDeliveryRecord = {
      deliveryId,
      reportId: verification.wire.reportId,
      endpoint: {
        endpointId: verification.wire.endpointId,
        groupId: verification.wire.endpointGroupId,
        url: options.candidate.endpointUrl,
      },
      wire: verification.wire,
      payload,
      payloadHash,
      contentType,
      status: 'approved',
      attempts: 0,
      revocationVisible: false,
      approvedSnapshotHash: options.snapshot.snapshotHash,
      approvedAt: options.now ?? options.snapshot.createdAt,
      approvedSnapshot: options.snapshot,
      candidate: verification.report,
      useDictionaryPayload,
    };
    this.records.set(deliveryId, record);
    return { ok: true, delivery: this.publicRecord(record) };
  }

  get(deliveryId: string): DeliveryRecord | undefined {
    const record = this.records.get(deliveryId);
    return record ? this.publicRecord(record) : undefined;
  }

  list(): readonly DeliveryRecord[] {
    return [...this.records.values()].map((record) => this.publicRecord(record));
  }

  observeSnapshot(snapshot: EvidenceSnapshot): readonly DeliveryRecord[] {
    this.latestSnapshot = snapshot;
    for (const record of this.records.values()) {
      if (record.status === 'acknowledged') {
        continue;
      }
      const check = checkContinuedAuthorization({
        record,
        currentSnapshot: snapshot,
      });
      record.latestCheck = check.ok ? 'approved' : check.status === 'pending' ? 'pending' : 'rejected';
      if (check.ok) {
        record.revocationVisible = false;
        if (record.status === 'held') {
          record.status = 'approved';
        }
      } else if (check.status === 'pending') {
        if (record.status !== 'in_flight') {
          record.status = 'held';
        }
      } else {
        record.revocationVisible = true;
        record.blockReason = check.reasons.join('; ');
        if (record.status !== 'in_flight') {
          record.status = 'suppressed';
        }
      }
    }
    return this.list();
  }

  beginAttempt(
    deliveryId: string,
    currentSnapshot: EvidenceSnapshot | null = this.latestSnapshot,
  ): BeginAttemptResult {
    const record = this.records.get(deliveryId);
    if (!record) {
      return { ok: false, status: 'unknown', reason: `unknown delivery ${deliveryId}` };
    }
    if (record.status === 'acknowledged') {
      return {
        ok: false,
        deliveryId,
        status: 'acknowledged',
        reason: 'delivery was already acknowledged',
      };
    }
    if (record.status === 'suppressed' || record.revocationVisible) {
      record.status = 'suppressed';
      return {
        ok: false,
        deliveryId,
        status: 'suppressed',
        reason: record.blockReason ?? 'new revocation is visible',
      };
    }
    if (record.status === 'in_flight') {
      return {
        ok: false,
        deliveryId,
        status: 'in_flight',
        reason: 'an attempt is already in flight; acknowledge or fail it first',
      };
    }

    const snapshot = currentSnapshot ?? this.latestSnapshot ?? record.approvedSnapshot;
    if (snapshot.snapshotHash !== record.approvedSnapshotHash) {
      const check = checkContinuedAuthorization({ record, currentSnapshot: snapshot });
      if (!check.ok) {
        if (check.status === 'pending') {
          record.status = 'held';
          return {
            ok: false,
            deliveryId,
            status: 'held',
            reason: check.missing.map((item) => item.reference).join(', ') || 'evidence pending',
          };
        }
        record.status = 'suppressed';
        record.revocationVisible = true;
        record.blockReason = check.reasons.join('; ');
        return {
          ok: false,
          deliveryId,
          status: 'suppressed',
          reason: record.blockReason,
        };
      }
    }

    if (fingerprint(record.payload) !== record.payloadHash) {
      record.status = 'suppressed';
      record.blockReason = 'sealed payload hash changed';
      return {
        ok: false,
        deliveryId,
        status: 'suppressed',
        reason: record.blockReason,
      };
    }

    record.status = 'in_flight';
    record.attempts += 1;
    return {
      ok: true,
      deliveryId,
      attempt: record.attempts,
      payload: record.payload,
      payloadHash: record.payloadHash,
      contentType: record.contentType,
      endpoint: record.endpoint,
    };
  }

  failAttempt(deliveryId: string, reason = 'transport_failure'): DeliveryRecord | undefined {
    const record = this.records.get(deliveryId);
    if (!record || record.status !== 'in_flight') {
      return record ? this.publicRecord(record) : undefined;
    }
    record.status = record.revocationVisible ? 'suppressed' : 'approved';
    record.blockReason = record.revocationVisible
      ? record.blockReason ?? reason
      : reason;
    return this.publicRecord(record);
  }

  acknowledge(deliveryId: string, acknowledgedAt?: number): DeliveryRecord | undefined {
    const record = this.records.get(deliveryId);
    if (!record) {
      return undefined;
    }
    if (record.status === 'suppressed') {
      return this.publicRecord(record);
    }
    record.status = 'acknowledged';
    record.acknowledgedAt = acknowledgedAt ?? record.approvedAt;
    return this.publicRecord(record);
  }

  private publicRecord(record: MutableDeliveryRecord): DeliveryRecord {
    return {
      deliveryId: record.deliveryId,
      reportId: record.reportId,
      endpoint: record.endpoint,
      wire: record.wire,
      payload: record.payload,
      payloadHash: record.payloadHash,
      contentType: record.contentType,
      status: record.status,
      attempts: record.attempts,
      revocationVisible: record.revocationVisible,
      approvedSnapshotHash: record.approvedSnapshotHash,
      approvedAt: record.approvedAt,
      ...(record.acknowledgedAt !== undefined
        ? { acknowledgedAt: record.acknowledgedAt }
        : {}),
      ...(record.blockReason !== undefined ? { blockReason: record.blockReason } : {}),
    };
  }
}

export interface ContinuedAuthorizationInput {
  readonly record: MutableDeliveryRecord;
  readonly currentSnapshot: EvidenceSnapshot;
}

export type ContinuedAuthorizationResult =
  | { readonly ok: true }
  | {
      readonly ok: false;
      readonly status: 'pending';
      readonly missing: readonly { reference: string; detail: string }[];
    }
  | {
      readonly ok: false;
      readonly status: 'rejected';
      readonly reasons: readonly string[];
    };

export function checkContinuedAuthorization(
  input: ContinuedAuthorizationInput,
): ContinuedAuthorizationResult {
  const { record, currentSnapshot } = input;
  const recipient: RecipientContext = {
    tenantId: record.wire.tenantId,
    principalId: record.wire.principalId,
    endpointId: record.wire.endpointId,
    endpointGroupId: record.wire.endpointGroupId,
  };

  const relevantMissingPages = currentSnapshot.knownMissingPages.filter((page) => {
    if (!page.scope) {
      return true;
    }
    return isScopeMatch(
      {
        tenantId: record.wire.tenantId,
        principalId: record.wire.principalId,
        topLevelSite: record.wire.topLevelSite,
        endpointGroupId: record.wire.endpointGroupId,
        documentGeneration: record.wire.documentGeneration,
      },
      { ...page.scope, bucketKey: undefined },
    );
  });
  if (relevantMissingPages.length > 0) {
    return {
      ok: false,
      status: 'pending',
      missing: relevantMissingPages.map((page) => ({
        reference: page.pageId,
        detail: page.reason ?? 'known event page is absent',
      })),
    };
  }

  const projection = buildPolicyProjection(currentSnapshot);
  const relevantPending = projection.causal.pending.filter((pending) => {
    if (record.candidate.proof.contributorIds.includes(pending.eventId)) {
      return true;
    }
    const event = currentSnapshot.byId[pending.eventId];
    if (!event) {
      return true;
    }
    if (event.tenantId !== record.wire.tenantId) {
      return false;
    }
    if ('principalId' in event && event.principalId !== record.wire.principalId) {
      return false;
    }
    return true;
  });
  if (relevantPending.length > 0) {
    return {
      ok: false,
      status: 'pending',
      missing: relevantPending.map((pending) => ({
        reference: pending.eventId,
        detail: pending.reason,
      })),
    };
  }

  const reasons: string[] = [];
  const proof = record.candidate.proof;
  const fieldRequirements: readonly (readonly [readonly string[], string])[] = [
    [proof.countEventIds, 'count'],
    [proof.earliestEventIds, 'earliestTimestamp'],
    [proof.bucketPresenceEventIds, 'bucketPresence'],
  ];

  for (const eventId of proof.contributorIds) {
    const currentEvent = currentSnapshot.byId[eventId];
    const approvedEvent = record.approvedSnapshot.byId[eventId];
    if (!currentEvent || !approvedEvent) {
      reasons.push(`contributor ${eventId} is missing from current evidence`);
      continue;
    }
    if (canonicalJson(currentEvent) !== canonicalJson(approvedEvent)) {
      reasons.push(`immutable evidence event ${eventId} changed`);
      continue;
    }
    if (currentEvent.kind !== 'failure') {
      reasons.push(`evidence ${eventId} is no longer a failure event`);
      continue;
    }
    const authorization = authorizeFailure(currentEvent, projection, recipient);
    if (!authorization.ok) {
      reasons.push(`${eventId}: ${authorization.reason}`);
    }
  }

  for (const [eventIds, field] of fieldRequirements) {
    for (const eventId of eventIds) {
      const event = currentSnapshot.byId[eventId];
      if (event?.kind !== 'failure') {
        reasons.push(`field provenance event ${eventId} is unavailable for ${field}`);
        continue;
      }
      const authorization = authorizeFailure(event, projection, recipient);
      if (authorization.ok && !authorization.fields.has(field)) {
        reasons.push(`${field} permission was withdrawn for ${eventId}`);
      }
    }
  }

  for (const [statusCode, eventIds] of Object.entries(proof.statusCodeEventIds)) {
    for (const eventId of eventIds) {
      const event = currentSnapshot.byId[eventId];
      if (event?.kind !== 'failure') {
        reasons.push(`status provenance event ${eventId} is unavailable`);
        continue;
      }
      const authorization = authorizeFailure(event, projection, recipient);
      if (authorization.ok && !authorization.fields.has('statusCodes')) {
        reasons.push(`statusCodes permission was withdrawn for ${eventId} (${statusCode})`);
      }
    }
  }

  if (record.wire.topLevelSite !== undefined) {
    for (const eventId of proof.contributorIds) {
      const event = currentSnapshot.byId[eventId];
      if (event?.kind === 'failure') {
        const authorization = authorizeFailure(event, projection, recipient);
        if (authorization.ok && !authorization.fields.has('topLevelSite')) {
          reasons.push(`topLevelSite permission was withdrawn for ${eventId}`);
        }
      }
    }
  }

  if (reasons.length > 0) {
    return { ok: false, status: 'rejected', reasons: [...new Set(reasons)] };
  }
  return { ok: true };
}
