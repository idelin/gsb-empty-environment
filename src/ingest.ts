import { buildReportPlan, type BuildReportPlanOptions } from './reports.ts';
import { createEvidenceSnapshot } from './snapshot.ts';
import type {
  EvidenceEvent,
  EvidenceSnapshot,
  IngestResult,
  MissingEventPage,
  UnixMillis,
} from './types.ts';

export interface IngestEvidenceOptions extends BuildReportPlanOptions {
  readonly knownMissingPages?: readonly MissingEventPage[];
  readonly createdAt?: UnixMillis;
}

export function ingestEvidence(
  previous: EvidenceSnapshot | null,
  incoming: EvidenceEvent | readonly EvidenceEvent[],
  options: IngestEvidenceOptions = {},
): IngestResult {
  const events: EvidenceEvent[] = [...(previous?.events ?? [])];
  const added = Array.isArray(incoming) ? incoming : [incoming];
  events.push(...added);

  const knownMissingPages =
    options.knownMissingPages ?? previous?.knownMissingPages ?? [];
  const snapshot = createEvidenceSnapshot(events, {
    knownMissingPages,
    createdAt: options.createdAt ?? previous?.createdAt ?? 0,
  });
  const plan = buildReportPlan(snapshot, options);

  return {
    snapshot,
    candidates: plan.candidates,
    denied: plan.analysis.denied,
    pending: plan.analysis.pending,
  };
}

export const receiveEventsAndBuildCandidates = ingestEvidence;
export const ingestEvents = ingestEvidence;
export const incrementallyAggregate = ingestEvidence;
export const aggregateEvents = ingestEvidence;
