/**
 * Core commit-decision logic for membership changes (joint consensus).
 *
 * Stages:
 *   stable(c)      -- normal operation, entries commit with a majority of c
 *   joint(o, n)    -- intermediate stage, entries commit only with a
 *                     majority of the old config AND a majority of the new
 *                     config. Config entries follow the same rules.
 */

export type MemberId = string;

export type Stage =
  | { readonly kind: "stable"; readonly config: readonly MemberId[] }
  | {
      readonly kind: "joint";
      readonly oldConfig: readonly MemberId[];
      readonly newConfig: readonly MemberId[];
    };

/** Smallest number of votes that forms a majority of `size` members. */
export function majorityOf(size: number): number {
  if (size <= 0) throw new Error("config must have at least one member");
  return Math.floor(size / 2) + 1;
}

/** True when `acks` covers a majority of `config`. */
export function isMajority(
  config: readonly MemberId[],
  acks: ReadonlySet<MemberId>,
): boolean {
  let count = 0;
  for (const m of config) if (acks.has(m)) count += 1;
  return count >= majorityOf(config.length);
}

/**
 * The commit decision. An entry (data or config) is committed in the given
 * stage iff the acknowledging voters satisfy the stage's quorum rule.
 */
export function isCommitted(
  stage: Stage,
  acks: ReadonlySet<MemberId>,
): boolean {
  switch (stage.kind) {
    case "stable":
      return isMajority(stage.config, acks);
    case "joint":
      return (
        isMajority(stage.oldConfig, acks) &&
        isMajority(stage.newConfig, acks)
      );
  }
}

/** Payloads carried by log entries. Config entries are log entries too. */
export type EntryPayload =
  | { readonly type: "data"; readonly value: string }
  /** C_old,new: entering the joint stage; committed under the OLD config rules. */
  | { readonly type: "config-joint"; readonly newConfig: readonly MemberId[] }
  /** C_new: leaving the joint stage; committed under the JOINT rules. */
  | { readonly type: "config-new" };

export interface LogEntry {
  readonly index: number;
  readonly term: number;
  readonly payload: EntryPayload;
}

/**
 * Configuration state machine. Transitions are driven exclusively by
 * *committed* config log entries:
 *
 *   stable(old) --commit(config-joint)--> joint(old, new)
 *   joint(old, new) --commit(config-new)--> stable(new)
 *
 * There is no direct stable(old) -> stable(new) edge: the intermediate
 * joint confirmation cannot be skipped.
 */
export class ConfigStateMachine {
  #stage: Stage;

  constructor(initialConfig: readonly MemberId[]) {
    if (initialConfig.length === 0) {
      throw new Error("initial config must not be empty");
    }
    this.#stage = { kind: "stable", config: [...initialConfig] };
  }

  get stage(): Stage {
    return this.#stage;
  }

  /** Leader proposes entering the joint stage (allowed only from stable). */
  proposeJoint(newConfig: readonly MemberId[]): LogEntry["payload"] {
    if (this.#stage.kind !== "stable") {
      throw new Error(
        "cannot propose a joint config while a reconfiguration is in flight",
      );
    }
    if (newConfig.length === 0) {
      throw new Error("new config must not be empty");
    }
    return { type: "config-joint", newConfig: [...newConfig] };
  }

  /** Leader proposes finalizing the switch (allowed only from joint). */
  proposeFinalize(): LogEntry["payload"] {
    if (this.#stage.kind !== "joint") {
      throw new Error("cannot finalize: not in the joint stage");
    }
    return { type: "config-new" };
  }

  /** Apply a config entry once it is committed. Data entries are ignored. */
  applyCommitted(entry: LogEntry): void {
    const p = entry.payload;
    if (p.type === "config-joint") {
      if (this.#stage.kind !== "stable") {
        throw new Error("config-joint committed outside stable stage");
      }
      this.#stage = {
        kind: "joint",
        oldConfig: this.#stage.config,
        newConfig: p.newConfig,
      };
    } else if (p.type === "config-new") {
      if (this.#stage.kind !== "joint") {
        throw new Error("config-new committed outside joint stage");
      }
      this.#stage = { kind: "stable", config: this.#stage.newConfig };
    }
  }

  /** Commit decision for this machine's current stage. */
  isCommitted(acks: ReadonlySet<MemberId>): boolean {
    return isCommitted(this.#stage, acks);
  }

  /**
   * UNSAFE: force stable(old) -> stable(new) without the joint stage.
   * Exists only so the counterexample can model a buggy implementation
   * that skips the intermediate confirmation. Never use in real code.
   */
  unsafeForceConfig(newConfig: readonly MemberId[]): void {
    if (this.#stage.kind !== "stable") {
      throw new Error("unsafeForceConfig expects a stable stage");
    }
    this.#stage = { kind: "stable", config: [...newConfig] };
  }
}
