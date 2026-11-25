import { KeyAdmissionEngine, type AdmissionReport, type IngestResult } from "./engine.ts";
import type { SignedEvent, Verifier } from "./events.ts";

/**
 * Offline verification entry point.
 *
 * Rebuilds admission from the uncompressed event log only. Every envelope is
 * retained (with its signature) and replayed through the same confluent
 * reducer as the online merger, which is why both entry points produce
 * identical reports for identical logical evidence under every legal delivery
 * order. Unknown or gapped evidence stays pending rather than being denied.
 */
export class OfflineVerifier {
  readonly #engine: KeyAdmissionEngine;

  constructor() {
    this.#engine = new KeyAdmissionEngine("offline");
  }

  get objectId(): string | undefined {
    return this.#engine.objectId;
  }

  /** Replay one full envelope. Safe to call in any order. */
  replay(envelope: SignedEvent, verify: Verifier): Promise<IngestResult> {
    return this.#engine.ingest(envelope, verify);
  }

  /** Replay a whole log regardless of how it was persisted. */
  async replayAll(envelopes: Iterable<SignedEvent>, verify: Verifier): Promise<IngestResult[]> {
    const results: IngestResult[] = [];
    for (const envelope of envelopes) results.push(await this.replay(envelope, verify));
    return results;
  }

  /** The retained uncompressed log, in stable storage order (not causal). */
  log(): SignedEvent[] {
    return this.#engine.envelopes();
  }

  gaps(): string[] {
    return this.#engine.gaps();
  }

  equivocatedActors(): string[] {
    return this.#engine.equivocatedActors();
  }

  canDiscardRevoke(revokeId: string): boolean {
    return this.#engine.canDiscardRevoke(revokeId);
  }

  /** Recompute the key handles a subject may claim from full evidence. */
  report(subject: string): AdmissionReport {
    return this.#engine.computeReport(subject);
  }

  reportAll(): AdmissionReport[] {
    return this.#engine.reportAll();
  }
}
