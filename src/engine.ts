/**
 * Multi-pattern composite event sequence matcher (CEP engine).
 *
 * Each registered pattern is compiled into a nondeterministic state graph
 * (a DAG of stages S0 -> S1 -> ... -> Sn(accept)). Every incoming event may
 * spawn a new run at S0 (nondeterministic "skip/take" semantics) while
 * existing runs advance independently, so overlapping matches are all found.
 *
 * Features:
 *  - Sequence patterns with negation: "A -> B with no C in between".
 *  - Cross-event sliding time window (first to last matched event).
 *  - Out-of-order compensation: a late-arriving forbidden event that falls
 *    inside an already-emitted match precisely retracts that match.
 *  - Pattern timeout: runs whose window has expired are discarded and logged.
 *  - A journal records every state transition for auditability.
 */

export interface CepEvent {
  id: string;
  type: string;
  /** Event time in milliseconds (may arrive out of order). */
  timestamp: number;
  data?: unknown;
}

export interface PatternDef {
  name: string;
  /** Ordered event types, e.g. ["A", "B"]. */
  sequence: string[];
  /** Event types that must NOT occur strictly between two matched events. */
  forbid?: string[];
  /** Max allowed span from the first to the last matched event, in ms. */
  windowMs: number;
}

export type Notification =
  | { kind: "match"; pattern: string; matchId: number; events: CepEvent[] }
  | {
      kind: "retract";
      pattern: string;
      matchId: number;
      events: CepEvent[];
      cause: CepEvent;
      reason: string;
    };

export type JournalKind =
  | "run-started"
  | "run-advanced"
  | "match"
  | "retract"
  | "invalidated"
  | "timeout";

export interface JournalEntry {
  /** Watermark (processing time) when the entry was written. */
  at: number;
  kind: JournalKind;
  pattern: string;
  message: string;
}

interface Run {
  runId: number;
  pattern: PatternDef;
  /** Index into pattern.sequence of the next stage to match. */
  stageIndex: number;
  matched: CepEvent[];
  alive: boolean;
}

interface CompletedMatch {
  matchId: number;
  pattern: PatternDef;
  events: CepEvent[];
  retracted: boolean;
}

interface BufferedEvent {
  event: CepEvent;
  seq: number;
}

export class CepEngine {
  private patterns: PatternDef[] = [];
  private runs: Run[] = [];
  private completed: CompletedMatch[] = [];
  private buffer: BufferedEvent[] = [];
  private listeners: Array<(n: Notification) => void> = [];
  readonly journal: JournalEntry[] = [];
  private watermark = Number.NEGATIVE_INFINITY;
  private nextRunId = 1;
  private nextMatchId = 1;
  private nextSeq = 1;

  registerPattern(def: PatternDef): void {
    if (!def.name) throw new Error("pattern name is required");
    if (def.sequence.length < 2)
      throw new Error("sequence must contain at least two stages");
    if (!(def.windowMs > 0)) throw new Error("windowMs must be positive");
    if (this.patterns.some((p) => p.name === def.name))
      throw new Error(`duplicate pattern: ${def.name}`);
    this.patterns.push({ ...def, forbid: def.forbid ?? [] });
  }

  subscribe(listener: (n: Notification) => void): void {
    this.listeners.push(listener);
  }

  /** Ingest one event. Events may arrive out of order by event time. */
  ingest(event: CepEvent): void {
    const entry: BufferedEvent = { event, seq: this.nextSeq++ };
    const pos = this.buffer.findIndex(
      (b) =>
        b.event.timestamp > event.timestamp ||
        (b.event.timestamp === event.timestamp && b.seq > entry.seq),
    );
    if (pos === -1) this.buffer.push(entry);
    else this.buffer.splice(pos, 0, entry);

    this.watermark = Math.max(this.watermark, event.timestamp);

    // 1. Negation: a forbidden event kills open runs and retracts matches
    //    whose matched span strictly contains its event time.
    for (const pattern of this.patterns) {
      if (!pattern.forbid!.includes(event.type)) continue;
      for (const run of this.runs) {
        if (!run.alive || run.pattern !== pattern) continue;
        if (run.matched[0].timestamp < event.timestamp) {
          run.alive = false;
          this.log("invalidated", pattern.name,
            `run#${run.runId} killed by forbidden ${event.type}@${event.timestamp}`);
        }
      }
      for (const match of this.completed) {
        if (match.retracted || match.pattern !== pattern) continue;
        const first = match.events[0].timestamp;
        const last = match.events[match.events.length - 1].timestamp;
        if (first < event.timestamp && event.timestamp < last) {
          match.retracted = true;
          const reason =
            `late forbidden ${event.type}@${event.timestamp} inside matched span (${first}, ${last})`;
          this.log("retract", pattern.name,
            `match#${match.matchId} retracted: ${reason}`);
          this.emit({
            kind: "retract",
            pattern: pattern.name,
            matchId: match.matchId,
            events: match.events,
            cause: event,
            reason,
          });
        }
      }
    }

    // 2. Advance existing runs waiting on this event type.
    for (const run of this.runs) {
      if (run.alive) this.applyEventToRun(run, event);
    }

    // 3. Spawn new runs at S0, then replay later buffered events so that
    //    a late-arriving first event still matches already-seen followers.
    for (const pattern of this.patterns) {
      if (pattern.sequence[0] !== event.type) continue;
      const run: Run = {
        runId: this.nextRunId++,
        pattern,
        stageIndex: 1,
        matched: [event],
        alive: true,
      };
      this.runs.push(run);
      this.log("run-started", pattern.name,
        `run#${run.runId} started by ${event.type}@${event.timestamp}`);
      for (const b of this.buffer) {
        if (!run.alive) break;
        if (b.event.timestamp > event.timestamp) this.applyEventToRun(run, b.event);
      }
    }

    this.runs = this.runs.filter((r) => r.alive);
  }

  /**
   * Advance the processing-time watermark. Runs whose window can no longer
   * be satisfied (watermark beyond start + windowMs) are discarded as timed
   * out and logged.
   */
  advanceTo(now: number): void {
    this.watermark = Math.max(this.watermark, now);
    for (const run of this.runs) {
      if (!run.alive) continue;
      const deadline = run.matched[0].timestamp + run.pattern.windowMs;
      if (this.watermark > deadline) {
        run.alive = false;
        this.log("timeout", run.pattern.name,
          `run#${run.runId} discarded: window expired at ${deadline}, watermark ${this.watermark}`);
      }
    }
    this.runs = this.runs.filter((r) => r.alive);
  }

  /** Try to advance (or kill) a single run with a single event. */
  private applyEventToRun(run: Run, event: CepEvent): void {
    const pattern = run.pattern;
    const last = run.matched[run.matched.length - 1];

    // Forbidden event strictly after the run's first match kills the run.
    if (pattern.forbid!.includes(event.type) &&
        run.matched[0].timestamp < event.timestamp) {
      run.alive = false;
      this.log("invalidated", pattern.name,
        `run#${run.runId} killed by forbidden ${event.type}@${event.timestamp}`);
      return;
    }

    if (pattern.sequence[run.stageIndex] !== event.type) return;
    if (event.timestamp < last.timestamp) return;
    if (event.timestamp - run.matched[0].timestamp > pattern.windowMs) return;
    // No forbidden event may sit strictly between the previous match and this one.
    if (this.hasForbiddenBetween(pattern, last.timestamp, event.timestamp)) return;

    run.matched.push(event);
    run.stageIndex += 1;

    if (run.stageIndex === pattern.sequence.length) {
      run.alive = false;
      const match: CompletedMatch = {
        matchId: this.nextMatchId++,
        pattern,
        events: [...run.matched],
        retracted: false,
      };
      this.completed.push(match);
      this.log("match", pattern.name,
        `match#${match.matchId} emitted: ` +
          match.events.map((e) => `${e.type}@${e.timestamp}`).join(" -> "));
      this.emit({
        kind: "match",
        pattern: pattern.name,
        matchId: match.matchId,
        events: match.events,
      });
    } else {
      this.log("run-advanced", pattern.name,
        `run#${run.runId} advanced to stage ${run.stageIndex} by ${event.type}@${event.timestamp}`);
    }
  }

  private hasForbiddenBetween(pattern: PatternDef, from: number, to: number): boolean {
    return this.buffer.some(
      (b) =>
        pattern.forbid!.includes(b.event.type) &&
        b.event.timestamp > from &&
        b.event.timestamp < to,
    );
  }

  private emit(n: Notification): void {
    for (const l of this.listeners) l(n);
  }

  private log(kind: JournalKind, pattern: string, message: string): void {
    this.journal.push({ at: this.watermark, kind, pattern, message });
  }
}
