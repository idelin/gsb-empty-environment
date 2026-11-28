/**
 * Multi-level escalation engine.
 *
 * Responsibilities:
 *  - Notify L1 immediately on trigger, then arm each subsequent level after its
 *    `delayMinutes`.
 *  - When a level resolves to an empty target set (restricted schedule has
 *    nobody on call), emit an empty-skip event and advance immediately instead
 *    of waiting. If every reachable level is empty, terminate with
 *    UNASSIGNABLE_ALERT without looping forever.
 *  - Support `numLoops` rewinds to L1 after the last level.
 *  - Ack cancels the pending escalation timer; Snooze pauses and re-arms on
 *    resume; Resolve terminates every scheduled event permanently.
 */

import { Clock, Scheduler, SystemClock, VirtualClock } from "./clock.js";
import { whoIsOnCall } from "./schedule.js";
import type {
  AlertState,
  EscalationEvent,
  EscalationPolicy,
  Schedule,
  UserId,
} from "./types.js";

const MS_PER_MINUTE = 60_000;

/** Resolves schedule/user targets to a de-duplicated set of on-call users. */
export interface TargetResolver {
  resolve(
    targets: EscalationPolicy["levels"][number]["targets"],
    atMs: number,
  ): UserId[];
}

/** Default resolver backed by the schedule solver. */
export class ScheduleTargetResolver implements TargetResolver {
  private readonly schedulesById = new Map<string, Schedule>();

  constructor(schedules: Schedule[]) {
    for (const schedule of schedules) {
      this.schedulesById.set(schedule.id, schedule);
    }
  }

  resolve(
    targets: EscalationPolicy["levels"][number]["targets"],
    atMs: number,
  ): UserId[] {
    const users = new Set<UserId>();
    for (const target of targets) {
      if (target.type === "user") {
        users.add(target.userId);
      } else {
        const schedule = this.schedulesById.get(target.scheduleId);
        if (!schedule) continue;
        const user = whoIsOnCall(schedule, atMs);
        if (user) users.add(user);
      }
    }
    return [...users];
  }
}

export interface Alert {
  id: string;
  state: AlertState;
  policy: EscalationPolicy;
  events: EscalationEvent[];
  /** Level index most recently notified, and the loop it belonged to. */
  currentLevelIndex: number;
  currentLoop: number;
  triggerAtMs: number;
}

export interface EngineOptions {
  clock?: Clock;
  resolver: TargetResolver;
}

/**
 * Stateful escalation engine bound to a clock. With a VirtualClock the driver
 * advances time and calls `pump()` to fire due timers; with a SystemClock real
 * timers are still processed through the same deterministic code path.
 */
export class EscalationEngine {
  private readonly clock: Clock;
  private readonly scheduler: Scheduler;
  private readonly resolver: TargetResolver;
  private readonly alerts = new Map<string, Alert>();
  private timerByAlert = new Map<string, number>();
  private snoozeTimerByAlert = new Map<string, number>();

  constructor(options: EngineOptions) {
    this.clock = options.clock ?? new SystemClock();
    this.resolver = options.resolver;
    this.scheduler = new Scheduler(this.clock);
  }

  /** Create an alert and immediately process its first level. */
  trigger(
    alertId: string,
    policy: EscalationPolicy,
    triggerAtMs: number = this.clock.now(),
  ): Alert {
    if (this.alerts.has(alertId)) {
      throw new Error(`Alert ${alertId} already exists`);
    }
    if (policy.levels.length === 0 || policy.numLoops < 1) {
      throw new Error("Policy must have at least one level and numLoops >= 1");
    }
    const alert: Alert = {
      id: alertId,
      state: "triggered",
      policy,
      events: [],
      currentLevelIndex: -1,
      currentLoop: 0,
      triggerAtMs,
    };
    this.alerts.set(alertId, alert);
    this.processFrom(alert, 0, 0);
    return alert;
  }

  getAlert(alertId: string): Alert | undefined {
    return this.alerts.get(alertId);
  }

  /** Acknowledge: cancel any pending escalation timer. */
  acknowledge(alertId: string, by: UserId): void {
    const alert = this.requireActive(alertId);
    this.cancelEscalationTimer(alert, "ack");
    alert.state = "acknowledged";
    alert.events.push({ type: "acknowledged", atMs: this.clock.now(), by });
  }

  /**
   * Snooze: cancel escalation timers, pause for `durationMs`, then return to
   * triggered and restart escalation timing from the current level.
   */
  snooze(alertId: string, durationMs: number): void {
    const alert = this.requireActive(alertId);
    if (durationMs <= 0) throw new Error("Snooze duration must be positive");
    this.cancelEscalationTimer(alert, "snooze");
    this.cancelSnoozeTimer(alertId);
    alert.state = "snoozed";
    const resumeAtMs = this.clock.now() + durationMs;
    alert.events.push({
      type: "snoozed",
      atMs: this.clock.now(),
      resumeAtMs,
    });
    const snoozeTimer = this.scheduler.schedule(durationMs, () => {
      this.resumeAfterSnooze(alertId);
    });
    this.snoozeTimerByAlert.set(alertId, snoozeTimer);
  }

  /** Resolve: permanently cancel every scheduled event. */
  resolve(alertId: string, by: UserId): void {
    const alert = this.alerts.get(alertId);
    if (!alert) throw new Error(`Unknown alert ${alertId}`);
    if (alert.state === "resolved") return;
    this.cancelEscalationTimer(alert, "resolve");
    this.cancelSnoozeTimer(alertId);
    alert.state = "resolved";
    alert.events.push({ type: "resolved", atMs: this.clock.now(), by });
  }

  /** Fire any timers due at the current clock time. */
  pump(): void {
    this.scheduler.runDue();
  }

  /** Advance a virtual clock and fire all timers crossed along the way. */
  advance(virtualClock: VirtualClock, durationMs: number): void {
    const target = virtualClock.now() + durationMs;
    for (;;) {
      const next = this.scheduler.nextDeadline();
      if (next === null || next > target) {
        virtualClock.setTime(target);
        this.scheduler.runDue();
        break;
      }
      virtualClock.setTime(next);
      this.scheduler.runDue();
    }
  }

  private resumeAfterSnooze(alertId: string): void {
    const alert = this.alerts.get(alertId);
    if (!alert || alert.state !== "snoozed") return;
    this.snoozeTimerByAlert.delete(alertId);
    alert.state = "triggered";
    alert.events.push({ type: "snooze_resumed", atMs: this.clock.now() });
    // Restart timing from the level after the one last notified.
    const levelCount = alert.policy.levels.length;
    this.armFollowing(
      alert,
      alert.currentLoop * levelCount + alert.currentLevelIndex,
    );
  }

  /**
   * Entry point used both at trigger (level 0, loop 0) and when a timer fires.
   * Empty levels are skipped immediately; the walk is bounded to guarantee
   * termination even when all levels are empty.
   */
  private processFrom(
    alert: Alert,
    startLevelIndex: number,
    loop: number,
  ): void {
    const levelCount = alert.policy.levels.length;
    const totalSlots = levelCount * alert.policy.numLoops;
    // Flatten (loop, level) into a single bounded position. Every slot is
    // visited at most once, so this walk can never spin forever.
    const first = this.findNextNonEmpty(
      alert,
      loop * levelCount + startLevelIndex,
      totalSlots,
    );
    if (!first) {
      this.markUnassignable(alert);
      return;
    }

    alert.currentLevelIndex = first.levelIndex;
    alert.currentLoop = first.loop;
    alert.events.push({
      type: "notified",
      atMs: this.clock.now(),
      levelId: first.level.id,
      loop: first.loop,
      users: first.users,
    });
    this.armFollowing(alert, first.position);
  }

  /**
   * Walk forward from `position`, recording an empty-skip event for each
   * restricted level encountered, until a level with users is found.
   */
  private findNextNonEmpty(
    alert: Alert,
    position: number,
    totalSlots: number,
  ):
    | {
        level: EscalationPolicy["levels"][number];
        levelIndex: number;
        loop: number;
        users: UserId[];
        position: number;
      }
    | null {
    const levelCount = alert.policy.levels.length;
    let current = position;
    while (current < totalSlots) {
      const currentLoop = Math.floor(current / levelCount);
      const levelIndex = current % levelCount;
      const level = alert.policy.levels[levelIndex];
      const users = this.resolver.resolve(level.targets, this.clock.now());
      if (users.length > 0) {
        return { level, levelIndex, loop: currentLoop, users, position: current };
      }
      alert.events.push({
        type: "level_skipped_empty",
        atMs: this.clock.now(),
        levelId: level.id,
        loop: currentLoop,
      });
      current += 1;
    }
    return null;
  }

  /**
   * After notifying the level at `notifiedPosition`, find the next non-empty
   * level slot and arm a single timer using that level's own delay. Target
   * resolution (and therefore empty-skip detection) is deferred until the timer
   * actually fires, because on-call state can change in the meantime.
   */
  private armFollowing(
    alert: Alert,
    notifiedPosition: number,
  ): void {
    const levelCount = alert.policy.levels.length;
    const totalSlots = levelCount * alert.policy.numLoops;
    const nextPosition = notifiedPosition + 1;
    if (nextPosition >= totalSlots) return; // loops exhausted.

    const targetLoop = Math.floor(nextPosition / levelCount);
    const targetIndex = nextPosition % levelCount;
    const nextLevel = alert.policy.levels[targetIndex];

    const delayMs = nextLevel.delayMinutes * MS_PER_MINUTE;
    const fireAtMs = this.clock.now() + delayMs;
    alert.events.push({
      type: "escalation_scheduled",
      atMs: this.clock.now(),
      fireAtMs,
      levelId: nextLevel.id,
      loop: targetLoop,
    });
    const timerId = this.scheduler.schedule(delayMs, () => {
      this.timerByAlert.delete(alert.id);
      if (alert.state === "resolved") return;
      this.processFrom(alert, targetIndex, targetLoop);
    });
    this.timerByAlert.set(alert.id, timerId);
  }

  private markUnassignable(alert: Alert): void {
    this.cancelEscalationTimer(alert, "resolve");
    alert.state = "unassignable";
    alert.events.push({ type: "unassignable", atMs: this.clock.now() });
  }

  private cancelEscalationTimer(
    alert: Alert,
    reason: "ack" | "resolve" | "snooze",
  ): void {
    const timerId = this.timerByAlert.get(alert.id);
    if (timerId !== undefined && this.scheduler.cancel(timerId)) {
      alert.events.push({
        type: "timer_cancelled",
        atMs: this.clock.now(),
        reason,
        levelId:
          alert.currentLevelIndex >= 0
            ? alert.policy.levels[alert.currentLevelIndex]?.id
            : undefined,
        loop: alert.currentLoop,
      });
    }
    this.timerByAlert.delete(alert.id);
  }

  private cancelSnoozeTimer(alertId: string): void {
    const timerId = this.snoozeTimerByAlert.get(alertId);
    if (timerId !== undefined) this.scheduler.cancel(timerId);
    this.snoozeTimerByAlert.delete(alertId);
  }

  private requireActive(alertId: string): Alert {
    const alert = this.alerts.get(alertId);
    if (!alert) throw new Error(`Unknown alert ${alertId}`);
    if (alert.state === "resolved" || alert.state === "unassignable") {
      throw new Error(`Alert ${alertId} is in terminal state ${alert.state}`);
    }
    return alert;
  }
}

export { VirtualClock };
