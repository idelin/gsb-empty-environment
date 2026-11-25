export type UnixMillis = number;

export type KnownDisclosableField =
  | 'count'
  | 'earliestTimestamp'
  | 'statusCodes'
  | 'bucketPresence'
  | 'batchBoundary'
  | 'topLevelSite'
  | 'policySource'
  | 'documentGeneration'
  | 'samplingCommitment';

export type DisclosableField = KnownDisclosableField | (string & {});

export const ALL_DISCLOSABLE_FIELDS: readonly KnownDisclosableField[] = [
  'count',
  'earliestTimestamp',
  'statusCodes',
  'bucketPresence',
  'batchBoundary',
  'topLevelSite',
  'policySource',
  'documentGeneration',
  'samplingCommitment',
];

export interface SamplingCommitment {
  readonly scheme: 'sample-promise-v1';
  readonly rate: number;
  readonly nonce: string;
  readonly included: boolean;
  readonly promise: string;
}

export interface EndpointRef {
  readonly endpointId: string;
  readonly groupId: string;
  readonly url: string;
}

export interface EvidenceScope {
  readonly tenantId?: string;
  readonly principalId?: string;
  readonly topLevelSite?: string;
  readonly endpointGroupId?: string;
  readonly documentGeneration?: string;
  readonly bucketKey?: string;
}

export interface MissingEventPage {
  readonly pageId: string;
  readonly eventIds?: readonly string[];
  readonly scope?: EvidenceScope;
  readonly reason?: string;
}

interface BaseEvidence {
  readonly id: string;
  readonly kind: string;
  readonly occurredAt: UnixMillis;
  readonly tenantId: string;
  readonly causalPrefix?: readonly string[];
  readonly pageId?: string;
  readonly ingestSequence?: number;
}

export interface FailureEvidence extends BaseEvidence {
  readonly kind: 'failure';
  readonly principalId: string;
  readonly policySourceId: string;
  readonly lineageId?: string;
  readonly topLevelSite: string;
  readonly documentHost?: string;
  readonly documentGeneration: string;
  readonly endpoint: EndpointRef;
  readonly bucketKey: string;
  readonly statusCode: number;
  readonly pageUrl?: string;
  readonly allowedFields: readonly DisclosableField[];
  readonly sampling: SamplingCommitment;
}

export interface PolicySpec {
  readonly policyId: string;
  readonly lineageId: string;
  readonly principalId: string;
  readonly tenantId: string;
  readonly enabled: boolean;
  readonly allowedFields: readonly DisclosableField[];
  readonly topLevelSites?: readonly string[];
  readonly sourceHost?: string;
  readonly includeSubdomains?: boolean;
  readonly endpoints?: readonly EndpointRef[];
  readonly batchMaxContributions?: number;
}

export interface PolicyReplacedEvent extends BaseEvidence {
  readonly kind: 'policy-replaced';
  readonly previousPolicyId?: string;
  readonly policy: PolicySpec;
}

export interface NavigationEvent extends BaseEvidence {
  readonly kind: 'navigation';
  readonly principalId: string;
  readonly topLevelSite: string;
  readonly fromGeneration?: string;
  readonly toGeneration: string;
  readonly destinationHost?: string;
  readonly revokePreviousReportPermission?: boolean;
}

export interface SubdomainInheritanceEvent extends BaseEvidence {
  readonly kind: 'subdomain-inheritance';
  readonly principalId: string;
  readonly policyId?: string;
  readonly parentHost: string;
  readonly childHost?: string;
  readonly enabled: boolean;
}

export interface EndpointGroupRevokedEvent extends BaseEvidence {
  readonly kind: 'endpoint-group-revoked';
  readonly principalId: string;
  readonly endpointGroupId: string;
  readonly reason?: string;
}

export interface ReportPermissionRevokedEvent extends BaseEvidence {
  readonly kind: 'report-permission-revoked';
  readonly principalId: string;
  readonly documentGeneration?: string;
  readonly endpointGroupId?: string;
  readonly reason?: string;
}

export type ControlEvidence =
  | PolicyReplacedEvent
  | NavigationEvent
  | SubdomainInheritanceEvent
  | EndpointGroupRevokedEvent
  | ReportPermissionRevokedEvent;

export type EvidenceEvent = FailureEvidence | ControlEvidence;

export interface EvidenceSnapshot {
  readonly schemaVersion: 1;
  readonly frozen: true;
  readonly snapshotHash: string;
  readonly createdAt: UnixMillis;
  readonly events: readonly EvidenceEvent[];
  readonly byId: Readonly<Record<string, EvidenceEvent>>;
  readonly order: readonly string[];
  readonly knownMissingPages: readonly MissingEventPage[];
}

export interface RecipientContext {
  readonly tenantId?: string;
  readonly principalId?: string;
  readonly endpointId?: string;
  readonly endpointGroupId?: string;
}

export interface WireBucket {
  readonly bucketKey?: string;
  readonly present?: true;
  readonly count?: number;
  readonly earliestTimestamp?: UnixMillis;
  readonly statusCodes?: readonly number[];
}

export interface WireReport {
  readonly schema: 'gsb.aggregate-report/v1';
  readonly reportId: string;
  readonly batchId: string;
  readonly batchIndex: number;
  readonly tenantId: string;
  readonly principalId: string;
  readonly endpointGroupId: string;
  readonly endpointId: string;
  readonly topLevelSite?: string;
  readonly policySource?: string;
  readonly documentGeneration?: string;
  readonly dictionaryId?: string;
  readonly buckets: readonly WireBucket[];
}

export interface StatusCodeProvenance {
  readonly [statusCode: string]: readonly string[];
}

export interface ReportProof {
  readonly schema: 'gsb.report-proof/v1';
  readonly reportHash: string;
  readonly authorizedEvidenceHash: string;
  readonly snapshotHash: string;
  readonly policyIds: readonly string[];
  readonly contributorIds: readonly string[];
  readonly countEventIds: readonly string[];
  readonly earliestEventIds: readonly string[];
  readonly statusCodeEventIds: StatusCodeProvenance;
  readonly bucketPresenceEventIds: readonly string[];
  readonly batch: {
    readonly index: number;
    readonly startOrdinal: number;
    readonly endOrdinal: number;
    readonly maxContributions: number;
    readonly state: 'open' | 'sealed';
  };
}

export interface EncodedReport {
  readonly algorithm: 'gsb.dict-ref/v1';
  readonly dictionaryId: string;
  readonly dictionaryHash: string;
  readonly value: unknown;
  readonly payload: string;
}

export interface CandidateReport extends WireReport {
  readonly endpointUrl: string;
  readonly state: 'open' | 'sealed';
  readonly proof: ReportProof;
  readonly encoded?: EncodedReport;
}

export interface CompressionDictionary {
  readonly kind: 'gsb.compression-dictionary/v1';
  readonly id: string;
  readonly hash: string;
  readonly scope: 'public-shared';
  readonly tokens: readonly string[];
}

export type AuthorizationReason =
  | 'unknown_policy'
  | 'policy_disabled'
  | 'tenant_mismatch'
  | 'principal_mismatch'
  | 'top_level_site_not_in_scope'
  | 'subdomain_not_in_scope'
  | 'document_generation_revoked'
  | 'endpoint_group_revoked'
  | 'endpoint_not_allowed'
  | 'recipient_mismatch'
  | 'no_disclosable_fields'
  | 'batch_boundary_not_allowed'
  | 'sampling_excluded'
  | 'invalid_sampling_commitment'
  | 'causal_evidence_pending';

export interface DeniedContribution {
  readonly eventId: string;
  readonly reason: AuthorizationReason;
}

export interface PendingEvidence {
  readonly eventId: string;
  readonly missingEventIds: readonly string[];
  readonly reason: 'missing_causal_prefix' | 'causal_cycle';
}

export interface IngestResult {
  readonly snapshot: EvidenceSnapshot;
  readonly candidates: readonly CandidateReport[];
  readonly denied: readonly DeniedContribution[];
  readonly pending: readonly PendingEvidence[];
}

export type VerificationResult =
  | {
      readonly status: 'verified';
      readonly report: CandidateReport;
      readonly wire: WireReport;
      readonly payload: string;
      readonly contentType: 'application/json';
    }
  | {
      readonly status: 'pending_attestation';
      readonly snapshotHash: string;
      readonly missing: readonly MissingEvidenceDescription[];
    }
  | {
      readonly status: 'rejected';
      readonly snapshotHash: string;
      readonly violations: readonly string[];
    };

export interface MissingEvidenceDescription {
  readonly kind: 'missing_page' | 'missing_causal_event';
  readonly reference: string;
  readonly detail: string;
}
