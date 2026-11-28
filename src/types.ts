/**
 * Domain model for the cross-DST on-call scheduling and multi-level
 * escalation engine.
 *
 * All absolute time values are UTC epoch milliseconds (the same unit as
 * `Date.now()`). Wall-clock rules (handoffs, restrictions) are expressed in
 * "local components" and interpreted against a layer's IANA time zone, so
 * daylight-saving transitions are handled through real tzdata rather than by
 * naively adding/subtracting 86400 seconds.
 */

/** A unique user identifier. */
export type UserId = string;

/** An IANA time zone name, e.g. "America/New_York". */
export type IanaTimeZone = string;

/** Rotation cadence of a base layer. */
export type RotationUnit = "daily" | "weekly" | "custom";

/**
 * Wall-clock components interpreted in a layer's IANA time zone.
 * `second`/`millisecond` default to zero.
 */
export interface WallTime {
  hour: number;
  minute: number;
  second?: number;
  millisecond?: number;
}

/**
 * A recurring restriction window expressed in local wall time.
 *
 * - `weekdays`: 0 = Sunday ... 6 = Saturday. The window opens on each listed
 *   weekday at `start` and closes at `end`.
 * - When `end <= start` the window crosses local midnight and is split into
 *   two UTC pieces automatically (e.g. 22:00 -> 06:00).
 */
export interface WeeklyRestriction {
  kind: "weekly";
  weekdays: number[];
  start: WallTime;
  end: WallTime;
}

/** Any supported restriction rule. */
export type Restriction = WeeklyRestriction;

/**
 * A base rotation layer.
 *
 * Members take turns in `members` order. The first member is on duty starting
 * at the UTC instant derived from `anchorLocal` (wall components in
 * `timeZone`). Each subsequent handoff advances local wall time by one
 * cadence step, which naturally yields 23h/25h real durations on DST
 * transition days instead of a fixed 86400s.
 */
export interface RotationLayer {
  id: string;
  /** Higher `priority` wins when layers overlap. */
  priority: number;
  timeZone: IanaTimeZone;
  members: UserId[];
  unit: RotationUnit;
  /** Used when unit === "custom"; length of one shift in milliseconds. */
  customDurationMs?: number;
  /** Local wall-time handoff point (time of day for daily/custom, weekday+time for weekly). */
  handoff: WallTime;
  /** For weekly rotations: weekday (0-6) of the weekly handoff. */
  handoffWeekday?: number;
  /** First-shift anchor as local wall components in `timeZone`. */
  anchorLocal: LocalDateTimeComponents;
  /** Optional restrictions that mask the rotation to on-call windows. */
  restrictions?: Restriction[];
}

/** Full calendar components in a specific IANA time zone. */
export interface LocalDateTimeComponents {
  year: number;
  /** Month 1-12. */
  month: number;
  /** Day of month 1-31. */
  day: number;
  hour: number;
  minute: number;
  second?: number;
  millisecond?: number;
}

/**
 * A temporary override. The replacement user is on duty for the closed-open
 * UTC interval [startMs, endMs). Overrides always beat rotation layers; among
 * overlapping overrides the one with higher `priority` (then later in array
 * order) wins.
 */
export interface Override {
  id: string;
  userId: UserId;
  startMs: number;
  endMs: number;
  priority?: number;
}

/** A complete schedule: ordered layers plus temporary overrides. */
export interface Schedule {
  id: string;
  layers: RotationLayer[];
  overrides?: Override[];
}

/** A half-open, non-overlapping on-call segment in UTC. */
export interface CoverageSegment {
  startMs: number;
  endMs: number;
  userId: UserId;
  /** Provenance of the winning rule, useful for debugging/auditing. */
  source:
    | { type: "override"; id: string }
    | { type: "layer"; id: string };
}

/** Target of an escalation level. */
export type EscalationTarget =
  | { type: "user"; userId: UserId }
  | { type: "schedule"; scheduleId: string };

/** One level of an escalation policy. */
export interface EscalationLevel {
  id: string;
  /** Delay before this level fires, measured from the (re)activation point. */
  delayMinutes: number;
  targets: EscalationTarget[];
}

/** A full escalation policy. */
export interface EscalationPolicy {
  id: string;
  levels: EscalationLevel[];
  /**
   * How many full passes through the level list are attempted. 1 means a
   * single pass (no looping); 2 means after the last level it rewinds to L1
   * once more, and so on. Must be >= 1.
   */
  numLoops: number;
}

/** Lifecycle state of an alert. */
export type AlertState =
  | "triggered"
  | "acknowledged"
  | "snoozed"
  | "resolved"
  | "unassignable";

/** Discriminated timeline events emitted by the escalation engine. */
export type EscalationEvent =
  | {
      type: "notified";
      atMs: number;
      levelId: string;
      /** Zero-based pass through the policy (0 = first loop). */
      loop: number;
      users: UserId[];
    }
  | {
      type: "level_skipped_empty";
      atMs: number;
      levelId: string;
      loop: number;
    }
  | {
      type: "escalation_scheduled";
      atMs: number;
      fireAtMs: number;
      levelId: string;
      loop: number;
    }
  | {
      type: "timer_cancelled";
      atMs: number;
      reason: "ack" | "resolve" | "snooze";
      levelId?: string;
      loop?: number;
    }
  | {
      type: "snoozed";
      atMs: number;
      resumeAtMs: number;
    }
  | {
      type: "snooze_resumed";
      atMs: number;
    }
  | {
      type: "acknowledged";
      atMs: number;
      by: UserId;
    }
  | {
      type: "resolved";
      atMs: number;
      by: UserId;
    }
  | {
      type: "unassignable";
      atMs: number;
    };
