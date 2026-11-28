/**
 * Multi-level escalation timeline engine.
 *
 * The engine is clock-agnostic: production uses the real wall clock while
 * tests drive a deterministic virtual clock, so every delay, skip, loop,
 * snooze and cancellation can be asserted on an exact UTC timeline.
 */

import { onCallAt, type Schedule, type UserId } from "./schedule.js";
import type { Disambiguation, Instant } from "./time.js";

export interface UserTarget {
  type: "user";
  user: UserId;
}

export interface ScheduleTarget {
  type: "schedule";
  scheduleId: string;
}

export type EscalationTarget = UserTarget | ScheduleTarget;

export interface EscalationLevel {
  /** Wait after notifying this level before evaluating the next one. */
  delayMinutes: number;
  targets: EscalationTarget[];
}

export interface EscalationPolicy {
  id: string;
  levels: EscalationLevel[];
  /**
   * Additional full passes after the initial pass. 0 means a single pass;
   * each loop rewinds from the last level back to L1.
   */
  numLoops?: number;
}

export type AlertState =
  | "triggered"
  | "acknowledged"
  | "snoozed"
  | "resolved"
  | "unassignable";

interface BaseEvent {
  alertId: string;
  at: Instant;
}

export interface NotifyEvent extends BaseEvent {
  type: "NOTIFY";
  levelIndex: number;
  loopIndex: number;
  users: UserId[];
}

export interface EmptyLevelSkipEvent extends BaseEvent {
  type: "EMPTY_LEVEL_SKIP";
  levelIndex: number;
  loopIndex: number;
}

export interface UnassignableEvent extends BaseEvent {
  type: "UNASSIGNABLE_ALERT";
}

export interface ExhaustedEvent extends BaseEvent {
  type: "ESCALATION_EXHAUSTED";
}

export interface AckEvent extends BaseEvent {
  type: "ACK";
  by: UserId | null;
}

export interface SnoozeStartEvent extends BaseEvent {
  type: "SNOOZE_START";
  until: Instant;
  by: UserId | null;
}

export interface SnoozeEndEvent extends BaseEvent {
  type: "SNOOZE_END";
}

export interface ResolveEvent extends BaseEvent {
  type: "RESOLVE";
  by: UserId | null;
}

export type EscalationEvent =
  | NotifyEvent
  | EmptyLevelSkipEvent
  | UnassignableEvent
  | ExhaustedEvent
  | AckEvent
  | SnoozeStartEvent
  | SnoozeEndEvent
  | ResolveEvent;

export interface Clock {
  now(): Instant;
  setTimeout(handler: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

/** Real process clock; delegates to the host timer loop. */
export class RealClock implements Clock {
  now(): Instant {
    return Date.now();
  }

  setTimeout(handler: () => void, delayMs: number): unknown {
    return setTimeout(handler, delayMs);
  }

  clearTimeout(handle: unknown): void {
    clearTimeout(handle);
  }
}

interface PendingTimer {
  id: number;
  at: Instant;
  handler: () => void;
}

/** Deterministic in-memory clock for tests and offline timeline simulation. */
export class VirtualClock implements Clock {
  private current: Instant;
  private readonly timers = new Map<number, PendingTimer>();
  private sequence = 0;

  constructor(initial: Instant) {
    this.current = initial;
  }

  now(): Instant {
    return this.current;
  }

  setTimeout(handler: () => void, delayMs: number): unknown {
    if (delayMs < 0) {
      throw new Error("Timer delay must be non-negative");
    }
    const id = (this.sequence += 1);
    this.timers.set(id, { id, at: this.current + delayMs, handler });
    return id;
  }

  clearTimeout(handle: unknown): void {
    if (typeof handle === "number") {
      this.timers.delete(handle);
    }
  }

  private earliest(): PendingTimer | undefined {
    let winner: PendingTimer | undefined;
    for (const timer of this.timers.values()) {
      if (winner === undefined || timer.at < winner.at) {
        winner = timer;
      }
    }
    return winner;
  }

  /** Advance virtual time by `durationMs`, firing every due timer in order. */
  advance(durationMs: number): void {
    const target = this.current + durationMs;
    for (;;) {
      const timer = this.earliest();
      if (timer === undefined || timer.at > target) {
        break;
      }
      this.current = timer.at;
      this.timers.delete(timer.id);
      timer.handler();
    }
    this.current = target;
  }

  /** Drain every currently scheduled timer, including ones armed mid-flight. */
  runAll(maxIterations = 10_000): void {
    let iterations = 0;
    while (this.timers.size > 0) {
      iterations += 1;
      if (iterations > maxIterations) {
        throw new Error("VirtualClock.runAll exceeded its iteration safety bound");
      }
      const timer = this.earliest();
      if (timer === undefined) {
        return;
      }
      this.current = timer.at;
      this.timers.delete(timer.id);
      timer.handler();
    }
  }

  get pendingCount(): number {
    return this.timers.size;
  }
}

export interface AlertHandle {
  readonly id: string;
  readonly state: AlertState;
  readonly events: readonly EscalationEvent[];
  ack(by?: UserId): void;
  snooze(durationMs: number, by?: UserId): void;
  resolve(by?: UserId): void;
}

interface MutableAlert {
  id: string;
  state: AlertState;
  events: EscalationEvent[];
  escalationTimer: unknown;
  wakeTimer: unknown;
}

export interface EngineOptions {
  schedules?: Iterable<Schedule>;
  clock?: Clock;
  disambiguation?: Disambiguation;
}

const MINUTE_MS = 60_000;

export class EscalationEngine {
  private readonly policy: EscalationPolicy;
  private readonly schedules = new Map<string, Schedule>();
  private readonly clock: Clock;
  private readonly disambiguation: Disambiguation;
  private readonly alerts = new Map<string, MutableAlert>();

  constructor(policy: EscalationPolicy, options: EngineOptions = {}) {
    if (policy.levels.length === 0) {
      throw new Error("Escalation policy must define at least one level");
    }
    this.policy = policy;
    for (const schedule of options.schedules ?? []) {
      this.schedules.set(schedule.id, schedule);
    }
    this.clock = options.clock ?? new RealClock();
    this.disambiguation = options.disambiguation ?? "compatible";
  }

  getAlert(id: string): AlertHandle {
    return this.handle(this.requireAlert(id));
  }

  triggerAlert(id: string): AlertHandle {
    if (this.alerts.has(id)) {
      throw new Error(`Alert "${id}" already exists`);
    }
    const alert: MutableAlert = {
      id,
      state: "triggered",
      events: [],
      escalationTimer: null,
      wakeTimer: null,
    };
    this.alerts.set(id, alert);
    this.beginPass(alert, 0);
    return this.handle(alert);
  }

  private handle(alert: MutableAlert): AlertHandle {
    return {
      id: alert.id,
      get state(): AlertState {
        return alert.state;
      },
      get events(): readonly EscalationEvent[] {
        return alert.events;
      },
      ack: (by?: UserId): void => this.acknowledge(alert, by ?? null),
      snooze: (durationMs: number, by?: UserId): void =>
        this.snooze(alert, durationMs, by ?? null),
      resolve: (by?: UserId): void => this.resolve(alert, by ?? null),
    };
  }

  private requireAlert(id: string): MutableAlert {
    const alert = this.alerts.get(id);
    if (alert === undefined) {
      throw new Error(`Unknown alert "${id}"`);
    }
    return alert;
  }

  private emit(alert: MutableAlert, event: EscalationEvent): void {
    alert.events.push(event);
  }

  private resolveTargets(level: EscalationLevel): UserId[] {
    const users: UserId[] = [];
    const seen = new Set<UserId>();
    const at = this.clock.now();
    for (const target of level.targets) {
      let resolved: UserId[];
      if (target.type === "user") {
        resolved = [target.user];
      } else {
        const schedule = this.schedules.get(target.scheduleId);
        if (schedule === undefined) {
          resolved = [];
        } else {
          const user = onCallAt(schedule, at, this.disambiguation);
          resolved = user === null ? [] : [user];
        }
      }
      for (const user of resolved) {
        if (!seen.has(user)) {
          seen.add(user);
          users.push(user);
        }
      }
    }
    return users;
  }

  /**
   * Evaluate levels from `startLevel` onward, instantly skipping empty ones.
   * A timer is armed only for a level that reaches a human. When the pass
   * exhausts every level without a notification, a first pass becomes
   * UNASSIGNABLE_ALERT; later passes are marked exhausted instead.
   */
  private beginPass(alert: MutableAlert, loopIndex: number, startLevel = 0): void {
    let notifiedInPass = false;

    for (let levelIndex = startLevel; levelIndex < this.policy.levels.length; levelIndex += 1) {
      const level = this.policy.levels[levelIndex];
      const users = this.resolveTargets(level);
      if (users.length === 0) {
        this.emit(alert, {
          type: "EMPTY_LEVEL_SKIP",
          alertId: alert.id,
          at: this.clock.now(),
          levelIndex,
          loopIndex,
        });
        continue;
      }

      notifiedInPass = true;
      this.emit(alert, {
        type: "NOTIFY",
        alertId: alert.id,
        at: this.clock.now(),
        levelIndex,
        loopIndex,
        users,
      });
      this.armEscalation(alert, level.delayMinutes, () => {
        this.afterDelay(alert, loopIndex, levelIndex);
      });
      return;
    }

    if (!notifiedInPass && loopIndex === 0 && !this.hasNotified(alert)) {
      alert.state = "unassignable";
      this.emit(alert, {
        type: "UNASSIGNABLE_ALERT",
        alertId: alert.id,
        at: this.clock.now(),
      });
      return;
    }

    this.emit(alert, {
      type: "ESCALATION_EXHAUSTED",
      alertId: alert.id,
      at: this.clock.now(),
    });
  }

  private hasNotified(alert: MutableAlert): boolean {
    return alert.events.some((event) => event.type === "NOTIFY");
  }

  private afterDelay(alert: MutableAlert, loopIndex: number, firedLevelIndex: number): void {
    if (alert.state !== "triggered") {
      return;
    }
    alert.escalationTimer = null;
    const nextLevel = firedLevelIndex + 1;

    if (nextLevel < this.policy.levels.length) {
      this.beginPass(alert, loopIndex, nextLevel);
      return;
    }

    const maxLoops = this.policy.numLoops ?? 0;
    if (loopIndex < maxLoops) {
      this.beginPass(alert, loopIndex + 1, 0);
    } else {
      this.emit(alert, {
        type: "ESCALATION_EXHAUSTED",
        alertId: alert.id,
        at: this.clock.now(),
      });
    }
  }

  private armEscalation(alert: MutableAlert, delayMinutes: number, handler: () => void): void {
    if (alert.escalationTimer !== null) {
      this.clock.clearTimeout(alert.escalationTimer);
    }
    alert.escalationTimer = this.clock.setTimeout(handler, delayMinutes * MINUTE_MS);
  }

  private acknowledge(alert: MutableAlert, by: UserId | null): void {
    if (alert.state === "resolved") {
      throw new Error(`Cannot acknowledge resolved alert "${alert.id}"`);
    }
    if (alert.state === "unassignable") {
      throw new Error(`Cannot acknowledge unassignable alert "${alert.id}"`);
    }
    if (alert.state === "acknowledged") {
      return;
    }
    if (alert.escalationTimer !== null) {
      this.clock.clearTimeout(alert.escalationTimer);
      alert.escalationTimer = null;
    }
    alert.state = "acknowledged";
    this.emit(alert, { type: "ACK", alertId: alert.id, at: this.clock.now(), by });
  }

  private snooze(alert: MutableAlert, durationMs: number, by: UserId | null): void {
    if (durationMs <= 0) {
      throw new Error("Snooze duration must be positive");
    }
    if (alert.state === "resolved" || alert.state === "unassignable") {
      throw new Error(`Cannot snooze alert "${alert.id}" in state ${alert.state}`);
    }
    if (alert.state === "snoozed") {
      throw new Error(`Alert "${alert.id}" is already snoozed`);
    }
    if (alert.escalationTimer !== null) {
      this.clock.clearTimeout(alert.escalationTimer);
      alert.escalationTimer = null;
    }
    alert.state = "snoozed";
    const until = this.clock.now() + durationMs;
    this.emit(alert, {
      type: "SNOOZE_START",
      alertId: alert.id,
      at: this.clock.now(),
      until,
      by,
    });
    alert.wakeTimer = this.clock.setTimeout(() => this.wakeFromSnooze(alert), durationMs);
  }

  private wakeFromSnooze(alert: MutableAlert): void {
    if (alert.state !== "snoozed") {
      return;
    }
    alert.wakeTimer = null;
    alert.state = "triggered";
    this.emit(alert, {
      type: "SNOOZE_END",
      alertId: alert.id,
      at: this.clock.now(),
    });
    this.beginPass(alert, 0);
  }

  private resolve(alert: MutableAlert, by: UserId | null): void {
    if (alert.state === "resolved") {
      return;
    }
    if (alert.escalationTimer !== null) {
      this.clock.clearTimeout(alert.escalationTimer);
      alert.escalationTimer = null;
    }
    if (alert.wakeTimer !== null) {
      this.clock.clearTimeout(alert.wakeTimer);
      alert.wakeTimer = null;
    }
    alert.state = "resolved";
    this.emit(alert, { type: "RESOLVE", alertId: alert.id, at: this.clock.now(), by });
  }
}
