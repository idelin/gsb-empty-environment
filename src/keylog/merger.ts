import {
  KeyAdmissionEngine,
  type AdmissionReport,
  type CompressedSummary,
  type IngestResult,
} from "./engine.ts";
import type { SignedEvent, Verifier } from "./events.ts";

/**
 * Online merge entry point.
 *
 * A long-lived sync host keeps only the evidence that can still change a
 * decision: compact records, the authoritative frontier, equivocation marks
 * and explicit gaps. Full envelopes and signatures are not retained; the host
 * verifies a signature once while merging, then works from fingerprints.
 */
export class OnlineMerger {
  readonly #engine: KeyAdmissionEngine;

  constructor(objectId?: string) {
    this.#engine = new KeyAdmissionEngine("online");
    if (objectId !== undefined) this.#engine.ingestSummary({
      version: 1,
      objectId,
      frontier: {},
      records: [],
      equivocated: {},
    });
  }

  get objectId(): string | undefined {
    return this.#engine.objectId;
  }

  merge(envelope: SignedEvent, verify: Verifier): Promise<IngestResult> {
    return this.#engine.ingest(envelope, verify);
  }

  /** Fold in a peer's compressed summary plus any incomplete suffix records. */
  mergeSummary(summary: CompressedSummary): void {
    this.#engine.ingestSummary(summary);
  }

  /** Minimal self-contained evidence to ship to another replica. */
  summary(): CompressedSummary {
    return this.#engine.toSummary();
  }

  /** Delivery gaps rendered as dot ids; never reported as non-emissions. */
  gaps(): string[] {
    return this.#engine.gaps();
  }

  equivocatedActors(): string[] {
    return this.#engine.equivocatedActors();
  }

  canDiscardRevoke(revokeId: string): boolean {
    return this.#engine.canDiscardRevoke(revokeId);
  }

  discardableRevokes(): string[] {
    return this.#engine.discardableRevokes();
  }

  report(subject: string): AdmissionReport {
    return this.#engine.computeReport(subject);
  }

  reportAll(): AdmissionReport[] {
    return this.#engine.reportAll();
  }
}
