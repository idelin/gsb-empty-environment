// Shared types for the consensus trace checker.
// Only erasable TypeScript syntax is used so Node >= 23.6 can run it directly.

export type NodeId = string;

/** Ballot with a total order: first by round, then by node id. */
export interface Ballot {
  round: number;
  node: NodeId;
}

export type EventKind =
  | 'promise'
  | 'accept'
  | 'persist'
  | 'restart'
  | 'learn'
  | 'apply';

/** A value accepted at a slot in a given ballot. */
export interface AcceptedValue {
  slot: number;
  ballot: Ballot;
  value: string; // value digest
}

/** Durable state recorded by a persist event, or recovered state on restart. */
export interface DurableState {
  promised?: Ballot;
  accepted?: AcceptedValue[];
}

export interface TraceEvent {
  id: string;
  kind: EventKind;
  node: NodeId;
  epoch: number;
  ballot?: Ballot; // promise / accept / learn
  slot?: number; // accept / learn / apply
  value?: string; // value digest: accept / learn / apply
  persisted?: DurableState; // persist
  recovered?: DurableState; // restart
  causes: string[]; // explicit causal references to other event ids
}

export const MEMBER_COUNT = 5;
export const QUORUM = 3;

export type FindingKind =
  | 'AcceptBelowPersistedPromise'
  | 'RestartLostPersistedState'
  | 'UnquorumedLearn'
  | 'ConflictingChosen'
  | 'ApplyGap'
  | 'ApplyValueMismatch'
  | 'ApplyNotChosen';

/** A self-contained claim that the independent verifier can re-check. */
export type ViolationClaim =
  | { kind: 'AcceptBelowPersistedPromise'; acceptId: string }
  | { kind: 'RestartLostPersistedState'; restartId: string }
  | { kind: 'UnquorumedLearn'; learnId: string }
  | { kind: 'ConflictingChosen'; slot: number }
  | { kind: 'ApplyGap'; applyId: string }
  | { kind: 'ApplyValueMismatch'; applyId: string }
  | { kind: 'ApplyNotChosen'; applyId: string };

export interface Evidence {
  claim: ViolationClaim;
  eventIds: string[]; // sorted lexicographically
  events: TraceEvent[]; // in original trace order
  minimalProven: boolean; // false when the search budget was exhausted
  budgetExhausted: boolean;
  verifierCalls: number;
}

export interface Finding {
  kind: FindingKind;
  severity: 'violation' | 'incomplete';
  eventIds: string[]; // root events of the finding
  missingCauses: string[]; // referenced but absent event ids
  detail: string;
  evidence?: Evidence; // present for violations
}

export interface CheckResult {
  ok: boolean; // true when no safety violation was found
  violations: Finding[];
  incomplete: Finding[];
  stats: { events: number; duplicatesIgnored: number };
}
