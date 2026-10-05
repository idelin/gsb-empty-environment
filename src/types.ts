export type Op = "create" | "update" | "delete";

export interface EventSpec {
  id: string;
  object: string;
  op: Op;
}

export interface Problem {
  events: EventSpec[];
  /** Pairs [before, after]: event `before` must appear earlier than `after`. */
  precedence?: Array<readonly [string, string]>;
}

export interface BlockedEvent {
  event: string;
  /** Direct predecessors that have not been emitted yet. */
  unmetPredecessors: string[];
  /** Why the event cannot be applied to the current object state, if applicable. */
  stateViolation: string | null;
}

export interface BlockingReport {
  /** Longest locally valid prefix the reorderer could emit (lexicographically greedy). */
  blockingPrefix: string[];
  remainingEvents: string[];
  blocked: BlockedEvent[];
  /** Remaining events involved in (or depending on) a precedence cycle. */
  precedenceCycle: string[];
  /** The constraints that block progress at the end of the blocking prefix. */
  constraints: {
    /** Unsatisfied precedence pairs among remaining events. */
    precedence: Array<[string, string]>;
    /** State-constraint violations for events whose predecessors are all met. */
    state: string[];
  };
}

export type SolveResult =
  | { ok: true; order: string[] }
  | ({ ok: false } & BlockingReport);

export class InvalidInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidInputError";
  }
}
