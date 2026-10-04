export type EventKind =
  | 'promise'
  | 'accept'
  | 'persist'
  | 'restart'
  | 'learn'
  | 'apply';

export interface TraceEvent {
  id: string;
  kind: EventKind;
  node: string;
  epoch: number;
  ballot: number;
  slot: number;
  value: string | null;
  causes: string[];
}

export type ViolationKind =
  | 'invalid_ballot'
  | 'accept_below_promise'
  | 'restart_state_loss'
  | 'learn_without_quorum'
  | 'conflicting_chosen'
  | 'apply_out_of_order'
  | 'apply_without_chosen';

export interface Claim {
  kind: ViolationKind;
  focus: string[];
  slot: number;
  node: string | null;
  value: string | null;
  detail: string;
}

export interface Evidence {
  eventIds: string[];
  events: TraceEvent[];
  minimal: boolean;
  budgetExhausted: boolean;
  verified: boolean;
}

export interface Violation {
  kind: ViolationKind;
  claim: Claim;
  detail: string;
  closure: string[];
  evidence: Evidence;
}

export interface IncompleteNote {
  eventId: string;
  missing: string[];
  reason: string;
}

export interface ConflictRecord {
  id: string;
  first: TraceEvent;
  second: TraceEvent;
}

export interface CheckReport {
  status: 'ok' | 'violations' | 'incomplete' | 'rejected';
  members: string[];
  quorum: number;
  eventCount: number;
  duplicateCount: number;
  errors: string[];
  conflicts: ConflictRecord[];
  violations: Violation[];
  incomplete: IncompleteNote[];
}

export interface CheckOptions {
  budget: number;
  members: string[];
}

export interface VerifyResult {
  ok: boolean;
  reason: string;
}
