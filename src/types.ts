/** A single capability offered by a party. */
export interface CapabilityDecl {
  /** Unique capability name, e.g. "codec.h265". */
  name: string;
  /** Preference weight; higher means more preferred. Defaults to 1. */
  priority?: number;
  /** Capabilities that must also be selected when this one is selected. */
  requires?: string[];
  /** Capabilities that must NOT be selected together with this one. */
  conflicts?: string[];
  /** If true, any acceptable configuration must include this capability. */
  mandatory?: boolean;
}

/** One party's declaration for a single negotiation round. */
export interface PartyDeclaration {
  /** Party identifier, e.g. "alice". */
  party: string;
  /**
   * Fresh nonce identifying this negotiation round. Reusing an old
   * declaration (replay) changes the bound transcript and is detected
   * during verification.
   */
  round: string;
  /** Optional issue time (epoch millis). Part of the bound declaration. */
  issuedAt?: number;
  capabilities: CapabilityDecl[];
}

/** A constraint extracted from the merged declarations. */
export interface ConflictConstraint {
  kind: 'mandatory' | 'requires' | 'conflicts';
  /** Subject capability (for requires/conflicts: the source endpoint). */
  capability: string;
  /** Target capability (only for requires/conflicts). */
  other?: string;
  /** Parties whose declarations contributed this constraint. */
  declaredBy: string[];
}

/** Minimal unsatisfiable core reported when no configuration exists. */
export interface ConflictReport {
  /** Deletion-minimal set of constraints that is jointly unsatisfiable. */
  constraints: ConflictConstraint[];
  /** Capabilities referenced by the core that are not commonly supported. */
  unsupported: string[];
  /** Human-readable explanation. */
  explanation: string;
}

/** Negotiation transcript binding the result to both original declarations. */
export interface Transcript {
  version: 1;
  /** Hash identifying this exact pair of declarations and rounds. */
  roundId: string;
  /** Both party ids, sorted. */
  parties: [string, string];
  /** party -> sha256 of the canonical declaration. */
  declarationHashes: { [party: string]: string };
  status: 'ok' | 'conflict';
  /** Selected configuration (sorted), null on conflict. */
  config: string[] | null;
  /** sha256 of the canonical config, null on conflict. */
  configHash: string | null;
  /** sha256 binding every field above. */
  digest: string;
}

export interface NegotiationOk {
  status: 'ok';
  /** Selected capability names, sorted. */
  config: string[];
  /** Total combined priority of the selected configuration. */
  score: number;
  transcript: Transcript;
}

export interface NegotiationConflict {
  status: 'conflict';
  conflict: ConflictReport;
  transcript: Transcript;
}

export type NegotiationResult = NegotiationOk | NegotiationConflict;

export interface TranscriptVerification {
  /** True when the transcript is self-consistent and binds both declarations. */
  valid: boolean;
  /**
   * Parties whose presented declaration does NOT match the declaration
   * bound in the transcript (replayed/stale or substituted declaration,
   * i.e. it belongs to another round).
   */
  replayed: string[];
  errors: string[];
}
