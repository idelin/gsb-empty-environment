// Assemble the full inference report: candidates (ranked by sample support,
// then parsimony), evidence with provenance, uncertainties, verified
// distinguishing instances, and an independent self-check that every
// retained sample is accepted by every emitted candidate.

import { buildEvidence, snapshotEvidence, type SampleStore } from "./evidence.ts";
import { countInductive, inferNode } from "./infer.ts";
import { buildDistinguishers } from "./distinguish.ts";
import { validate } from "./validate.ts";
import type { Candidate, Distinguisher, Schema, Uncertainty } from "./types.ts";

export const DISCLAIMERS: string[] = [
  "All inferences are inductive over the retained samples only; nothing here is a universal guarantee about unobserved data.",
  "Properties never observed are reported as unknown, never forbidden (no additionalProperties:false is emitted).",
  "Only the implemented subset is promised: type (incl. unions), properties, required, items, minimum/maximum, minLength/maxLength, minItems/maxItems, enum.",
  "Observing only integers does not rule out future decimals; observing a value in every sample does not rule out future absence; null and missing are counted separately.",
];

export interface Report {
  status: "inferred" | "unknown";
  reason?: string;
  sampleCount: number;
  provenance: { retained: string[]; retracted: string[] };
  candidates: Candidate[];
  evidence: unknown;
  uncertainties: Uncertainty[];
  distinguishers: Distinguisher[];
  disclaimers: string[];
}

function selfCheck(
  profile: Candidate["profile"],
  schema: Schema,
  samples: [string, unknown][],
): Candidate {
  const failures: Candidate["selfCheck"]["failures"] = [];
  for (const [id, sample] of samples) {
    const errors = validate(sample, schema);
    if (errors.length > 0) failures.push({ sampleId: id, errors });
  }
  return {
    rank: 0,
    profile,
    schema,
    support: { accepted: samples.length - failures.length, total: samples.length },
    inductiveAssumptions: countInductive(schema),
    selfCheck: { allRetainedSamplesAccepted: failures.length === 0, failures },
  };
}

export function buildReport(store: SampleStore): Report {
  const provenance = { retained: store.retainedIds(), retracted: store.retractedIds() };
  const evidence = buildEvidence(store);

  if (!evidence) {
    return {
      status: "unknown",
      reason: "no retained samples; an empty sample set implies nothing",
      sampleCount: 0,
      provenance,
      candidates: [],
      evidence: null,
      uncertainties: [],
      distinguishers: [],
      disclaimers: DISCLAIMERS,
    };
  }

  const { strict, generalized, axes, uncertainties } = inferNode(evidence, "#");
  const samples = store.retained();

  const candidates = [
    selfCheck("generalized", generalized, samples),
    selfCheck("observed-fit", strict, samples),
  ];
  // Rank: sample support first, then parsimony (fewer inductive assumptions).
  candidates.sort(
    (a, b) =>
      b.support.accepted - a.support.accepted ||
      a.inductiveAssumptions - b.inductiveAssumptions,
  );
  candidates.forEach((c, i) => {
    c.rank = i + 1;
  });

  return {
    status: "inferred",
    sampleCount: store.size,
    provenance,
    candidates,
    evidence: snapshotEvidence(evidence),
    uncertainties,
    distinguishers: buildDistinguishers(axes, strict, generalized),
    disclaimers: DISCLAIMERS,
  };
}
