/**
 * Deterministic simulator driving leaders through a membership change.
 *
 * The simulator is intentionally small: a leader appends one entry at a
 * time, collects a caller-specified set of acknowledgements, and evaluates
 * the commit decision for its current stage. Committed data entries are
 * recorded in a SafetyTracker so that conflicting commits at the same log
 * index (split brain) are detected.
 */

import {
  ConfigStateMachine,
  isMajority,
  type EntryPayload,
  type LogEntry,
  type MemberId,
  type Stage,
} from "./consensus.ts";

export type RegisterResult =
  | { readonly ok: true }
  | { readonly ok: false; readonly existing: string; readonly incoming: string };

/** Detects two different values committed at the same log index. */
export class SafetyTracker {
  #committed = new Map<number, string>();

  register(entry: LogEntry, value: string): RegisterResult {
    const existing = this.#committed.get(entry.index);
    if (existing !== undefined && existing !== value) {
      return { ok: false, existing, incoming: value };
    }
    this.#committed.set(entry.index, value);
    return { ok: true };
  }

  committedAt(index: number): string | undefined {
    return this.#committed.get(index);
  }
}

export class Leader {
  readonly id: MemberId;
  readonly fsm: ConfigStateMachine;
  readonly tracker: SafetyTracker;
  readonly log: LogEntry[] = [];
  term: number;
  commitIndex = 0;

  constructor(
    id: MemberId,
    initialConfig: readonly MemberId[],
    tracker: SafetyTracker,
    term: number,
  ) {
    this.id = id;
    this.fsm = new ConfigStateMachine(initialConfig);
    this.tracker = tracker;
    this.term = term;
  }

  get stage(): Stage {
    return this.fsm.stage;
  }

  /** Append an entry and try to commit it with the given acknowledgements. */
  #appendAndTryCommit(
    payload: EntryPayload,
    ackers: readonly MemberId[],
    commitRule: (acks: ReadonlySet<MemberId>) => boolean,
  ): { entry: LogEntry; committed: boolean } {
    const entry: LogEntry = {
      index: this.log.length + 1,
      term: this.term,
      payload,
    };
    this.log.push(entry);
    const acks = new Set<MemberId>([this.id, ...ackers]);
    if (!commitRule(acks)) return { entry, committed: false };
    this.commitIndex = entry.index;
    if (payload.type === "data") {
      const result = this.tracker.register(entry, payload.value);
      if (!result.ok) {
        throw new Error(
          `SAFETY VIOLATION: index ${entry.index} committed as ` +
            `"${result.existing}" and again as "${result.incoming}"`,
        );
      }
    } else {
      this.fsm.applyCommitted(entry);
    }
    return { entry, committed: true };
  }

  /** Replicate a data entry; commits under the current stage's rule. */
  proposeData(value: string, ackers: readonly MemberId[]): boolean {
    return this.#appendAndTryCommit(
      { type: "data", value },
      ackers,
      (acks) => this.fsm.isCommitted(acks),
    ).committed;
  }

  /**
   * Step 1 of the switch: propose C_old,new. It is a log entry like any
   * other, so it commits under the CURRENT (old, stable) stage rules.
   */
  proposeJointConfig(
    newConfig: readonly MemberId[],
    ackers: readonly MemberId[],
  ): boolean {
    const payload = this.fsm.proposeJoint(newConfig);
    return this.#appendAndTryCommit(payload, ackers, (acks) =>
      this.fsm.isCommitted(acks),
    ).committed;
  }

  /**
   * Step 2 of the switch: propose C_new. It commits under the JOINT stage
   * rules, i.e. it needs a majority of BOTH the old and the new config.
   */
  finalizeConfig(ackers: readonly MemberId[]): boolean {
    const payload = this.fsm.proposeFinalize();
    return this.#appendAndTryCommit(payload, ackers, (acks) =>
      this.fsm.isCommitted(acks),
    ).committed;
  }

  /**
   * BUGGY shortcut used only to demonstrate why the joint stage exists:
   * commit C_new directly under the OLD config's majority rule and jump
   * straight to the new config, skipping joint confirmation entirely.
   */
  unsafeDirectSwitch(
    newConfig: readonly MemberId[],
    ackers: readonly MemberId[],
  ): boolean {
    const stage = this.fsm.stage;
    if (stage.kind !== "stable") {
      throw new Error("unsafeDirectSwitch expects a stable stage");
    }
    const oldConfig = stage.config;
    const entry: LogEntry = {
      index: this.log.length + 1,
      term: this.term,
      payload: { type: "config-new" },
    };
    this.log.push(entry);
    const acks = new Set<MemberId>([this.id, ...ackers]);
    if (!isMajority(oldConfig, acks)) return false;
    this.commitIndex = entry.index;
    // Force the illegal stable(old) -> stable(new) transition.
    this.fsm.unsafeForceConfig(newConfig);
    return true;
  }
}
