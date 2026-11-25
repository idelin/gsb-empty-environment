import { DeliveryOutbox, type ApproveDeliveryResult, type BeginAttemptResult, type DeliveryRecord } from './delivery.ts';
import { ingestEvidence, type IngestEvidenceOptions } from './ingest.ts';
import { verifyReportForDelivery, type VerifyReportOptions } from './reports.ts';
import { createEvidenceSnapshot } from './snapshot.ts';
import type {
  CandidateReport,
  EvidenceEvent,
  EvidenceSnapshot,
  IngestResult,
  MissingEventPage,
  VerificationResult,
} from './types.ts';

export interface ReportingHostOptions extends Omit<IngestEvidenceOptions, 'createdAt'> {
  readonly now?: () => number;
}

export class ReportingHost {
  readonly outbox = new DeliveryOutbox();
  snapshot: EvidenceSnapshot;
  candidates: readonly CandidateReport[] = [];
  lastIngest: IngestResult | null = null;
  private readonly options: ReportingHostOptions;

  constructor(options: ReportingHostOptions = {}) {
    this.options = options;
    this.snapshot = createEvidenceSnapshot([], {
      createdAt: options.now?.() ?? 0,
      knownMissingPages: options.knownMissingPages,
    });
  }

  ingest(
    events: EvidenceEvent | readonly EvidenceEvent[],
    overrides: Partial<IngestEvidenceOptions> = {},
  ): IngestResult {
    const { now: _now, ...options } = this.options;
    const result = ingestEvidence(this.snapshot, events, {
      ...options,
      createdAt: this.options.now?.() ?? this.snapshot.createdAt + 1,
      ...overrides,
    });
    this.snapshot = result.snapshot;
    this.candidates = result.candidates;
    this.lastIngest = result;
    this.outbox.observeSnapshot(this.snapshot);
    return result;
  }

  markEventPageMissing(page: MissingEventPage): IngestResult {
    return this.ingest([], {
      knownMissingPages: [...this.snapshot.knownMissingPages, page],
    });
  }

  verify(
    report: VerifyReportOptions['report'],
    overrides: Partial<VerifyReportOptions> = {},
  ): VerificationResult {
    return verifyReportForDelivery({
      report,
      snapshot: this.snapshot,
      dictionary: this.options.dictionary,
      ...overrides,
    });
  }

  approve(
    candidate: CandidateReport,
    overrides: Partial<Parameters<DeliveryOutbox['approve']>[0]> = {},
  ): ApproveDeliveryResult {
    return this.outbox.approve({
      candidate,
      snapshot: this.snapshot,
      dictionary: this.options.dictionary,
      ...overrides,
    });
  }

  beginDelivery(deliveryId: string): BeginAttemptResult {
    return this.outbox.beginAttempt(deliveryId, this.snapshot);
  }

  failDelivery(deliveryId: string, reason?: string): DeliveryRecord | undefined {
    return this.outbox.failAttempt(deliveryId, reason);
  }

  acknowledgeDelivery(deliveryId: string, now?: number): DeliveryRecord | undefined {
    return this.outbox.acknowledge(deliveryId, now ?? this.options.now?.());
  }
}
