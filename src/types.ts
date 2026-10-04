/** Event types participating in the pattern. */
export type EventType = 'A' | 'B' | 'C';

/**
 * A single revision of an event. Events have a stable `id`; a higher
 * `revision` may change the type, move the event time, or retract it.
 */
export interface EventRevision {
  id: string;
  revision: number;
  type: EventType;
  timestamp: number;
  retracted?: boolean;
}

export type ChangeKind = 'upsert' | 'retract';

/** One entry of the output change stream. `seq` is strictly increasing. */
export interface Change {
  seq: number;
  kind: ChangeKind;
  alertId: string;
  aId: string;
  bId: string;
}

export type ApplyStatus =
  | 'applied'   // highest revision so far; accepted (state may or may not change)
  | 'duplicate' // same revision, identical content: idempotent no-op
  | 'stale'     // lower revision than the stored one: ignored
  | 'conflict'  // same revision, different content: rejected, state untouched
  | 'late';     // beyond allowed lateness: rejected, state untouched

export interface ApplyResult {
  status: ApplyStatus;
  changes: Change[];
}

export interface MatcherOptions {
  /** Inclusive window W: a pair needs tB - tA <= windowMs. */
  windowMs: number;
  /** Revisions with timestamp < watermark - allowedLateness are rejected. */
  allowedLateness: number;
}

export function alertIdOf(aId: string, bId: string): string {
  return `alert:${aId}->${bId}`;
}
