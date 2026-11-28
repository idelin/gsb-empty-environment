/**
 * Multi-layer on-call schedule resolution.
 *
 * A schedule is the superposition of, in increasing precedence:
 *   1. base rotation layers (larger `priority` wins),
 *   2. temporary overrides (always win over every layer).
 * Layers hand over at wall-clock instants in their own IANA zone, and may be
 * clipped by weekly restriction windows. The output is a normalized,
 * non-overlapping UTC timeline; uncovered intervals (e.g. outside a
 * restriction) remain explicit gaps.
 */

import {
  addWallDuration,
  localWeekday,
  resolveWall,
  wallPartsAt,
  type Disambiguation,
  type Instant,
  type WallDuration,
} from "./time.js";

export type UserId = string;

export interface RotationLength {
  days?: number;
  hours?: number;
  minutes?: number;
}

export interface RotationLayer {
  id: string;
  timeZone: string;
  /** Ordered rotation members; layer[i] owns shift i, cycling from `start`. */
  users: UserId[];
  /** UTC instant of the first handoff (the rotation anchor). */
  start: Instant;
  /** Shift length expressed in local wall time. */
  shiftLength: RotationLength;
  /** Higher value means higher precedence when layers overlap. */
  priority: number;
  restrictions?: Restriction[];
}

/** Weekly restriction in the layer's local wall time. */
export interface Restriction {
  /** 0 = Sunday ... 6 = Saturday. */
  startWeekday: number;
  startHour: number;
  startMinute?: number;
  durationMinutes: number;
}

export interface Override {
  id: string;
  /** Inclusive UTC start. */
  start: Instant;
  /** Exclusive UTC end. */
  end: Instant;
  user: UserId;
}

export interface Schedule {
  id: string;
  timeZone: string;
  layers: RotationLayer[];
  overrides?: Override[];
}

export interface TimelineSegment {
  start: Instant;
  end: Instant;
  user: UserId;
  source: string;
}

export interface TimelineGap {
  start: Instant;
  end: Instant;
  user: null;
}

export type TimelineEntry = TimelineSegment | TimelineGap;

interface RawSegment {
  start: Instant;
  end: Instant;
  user: UserId;
  source: string;
  precedence: number;
}

function asWallDuration(length: RotationLength): WallDuration {
  return {
    days: length.days,
    hours: length.hours,
    minutes: length.minutes,
  };
}

/** All handoff instants for a layer that intersect [windowStart, windowEnd). */
function handoffInstants(
  layer: RotationLayer,
  windowStart: Instant,
  windowEnd: Instant,
  disambiguation: Disambiguation,
): Instant[] {
  const duration = asWallDuration(layer.shiftLength);
  const handoffs: Instant[] = [layer.start];

  let forward = layer.start;
  while (forward < windowEnd) {
    const next = addWallDuration(layer.timeZone, forward, duration, disambiguation);
    if (next <= forward) {
      throw new Error(`Rotation "${layer.id}" has a non-positive shift length`);
    }
    forward = next;
    handoffs.push(forward);
  }

  let backward = layer.start;
  while (backward > windowStart) {
    const previous = addWallDuration(layer.timeZone, backward, negate(duration), disambiguation);
    if (previous >= backward) {
      break;
    }
    backward = previous;
    handoffs.unshift(backward);
  }

  return handoffs;
}

function negate(duration: WallDuration): WallDuration {
  return {
    days: -(duration.days ?? 0),
    hours: -(duration.hours ?? 0),
    minutes: -(duration.minutes ?? 0),
    seconds: -(duration.seconds ?? 0),
    milliseconds: -(duration.milliseconds ?? 0),
  };
}

function rotationUser(layer: RotationLayer, shiftIndex: number): UserId {
  const length = layer.users.length;
  const normalized = ((shiftIndex % length) + length) % length;
  return layer.users[normalized];
}

/** UTC segments contributed by weekly restriction windows inside the window. */
function restrictionSegments(
  layer: RotationLayer,
  windowStart: Instant,
  windowEnd: Instant,
  disambiguation: Disambiguation,
): Array<{ start: Instant; end: Instant }> {
  const segments: Array<{ start: Instant; end: Instant }> = [];
  const restrictions = layer.restrictions ?? [];
  if (restrictions.length === 0) {
    return [{ start: windowStart, end: windowEnd }];
  }

  const firstWall = wallPartsAt(layer.timeZone, windowStart);
  const scanStart = resolveWall(
    layer.timeZone,
    {
      year: firstWall.year,
      month: firstWall.month,
      day: firstWall.day,
      hour: 0,
      minute: 0,
      second: 0,
      millisecond: 0,
    },
    disambiguation,
  ).instant;

  for (let dayOffset = -1; dayOffset <= 8; dayOffset += 1) {
    const midnight = addWallDuration(
      layer.timeZone,
      scanStart,
      { days: dayOffset },
      disambiguation,
    );
    const weekday = localWeekday(layer.timeZone, midnight);

    for (const restriction of restrictions) {
      if (weekday !== restriction.startWeekday) {
        continue;
      }
      const start = addWallDuration(
        layer.timeZone,
        midnight,
        {
          hours: restriction.startHour,
          minutes: restriction.startMinute ?? 0,
        },
        disambiguation,
      );
      const end = addWallDuration(
        layer.timeZone,
        start,
        { minutes: restriction.durationMinutes },
        disambiguation,
      );
      const clippedStart = Math.max(start, windowStart);
      const clippedEnd = Math.min(end, windowEnd);
      if (clippedStart < clippedEnd) {
        segments.push({ start: clippedStart, end: clippedEnd });
      }
    }
  }

  return mergeIntervals(segments);
}

function mergeIntervals(
  intervals: Array<{ start: Instant; end: Instant }>,
): Array<{ start: Instant; end: Instant }> {
  const sorted = [...intervals].sort((a, b) => a.start - b.start);
  const merged: Array<{ start: Instant; end: Instant }> = [];
  for (const interval of sorted) {
    const last = merged[merged.length - 1];
    if (last !== undefined && interval.start <= last.end) {
      last.end = Math.max(last.end, interval.end);
    } else {
      merged.push({ ...interval });
    }
  }
  return merged;
}

function layerSegments(
  layer: RotationLayer,
  windowStart: Instant,
  windowEnd: Instant,
  disambiguation: Disambiguation,
): RawSegment[] {
  const handoffs = handoffInstants(layer, windowStart, windowEnd, disambiguation);
  const allowed = restrictionSegments(layer, windowStart, windowEnd, disambiguation);
  const segments: RawSegment[] = [];

  for (let index = 0; index < handoffs.length - 1; index += 1) {
    const shiftStart = Math.max(handoffs[index], windowStart);
    const shiftEnd = Math.min(handoffs[index + 1], windowEnd);
    if (shiftStart >= shiftEnd) {
      continue;
    }
    const user = rotationUser(layer, indexWithAnchor(layer, handoffs, index));

    for (const interval of allowed) {
      const start = Math.max(shiftStart, interval.start);
      const end = Math.min(shiftEnd, interval.end);
      if (start < end) {
        segments.push({
          start,
          end,
          user,
          source: layer.id,
          precedence: layer.priority,
        });
      }
    }
  }

  return segments;
}

function indexWithAnchor(layer: RotationLayer, handoffs: Instant[], index: number): number {
  const anchorIndex = handoffs.indexOf(layer.start);
  return index - (anchorIndex === -1 ? 0 : anchorIndex);
}

/**
 * Build the normalized UTC timeline of a schedule over [start, end).
 *
 * Boundaries from every layer, restriction and override are collected into a
 * sweep-line; at each elementary interval the highest-precedence coverage
 * wins. Adjacent intervals with the same winning user and source are merged,
 * including an override that splits a layer shift into three pieces.
 */
export function buildTimeline(
  schedule: Schedule,
  start: Instant,
  end: Instant,
  disambiguation: Disambiguation = "compatible",
): TimelineEntry[] {
  if (!(end > start)) {
    throw new Error("Timeline window end must be strictly after start");
  }

  const raw: RawSegment[] = [];
  for (const layer of schedule.layers) {
    raw.push(...layerSegments(layer, start, end, disambiguation));
  }
  for (const override of schedule.overrides ?? []) {
    const segmentStart = Math.max(override.start, start);
    const segmentEnd = Math.min(override.end, end);
    if (segmentStart < segmentEnd) {
      raw.push({
        start: segmentStart,
        end: segmentEnd,
        user: override.user,
        source: override.id,
        precedence: Number.POSITIVE_INFINITY,
      });
    }
  }

  const boundaries = new Set<Instant>([start, end]);
  for (const segment of raw) {
    boundaries.add(segment.start);
    boundaries.add(segment.end);
  }
  const sortedBoundaries = [...boundaries].sort((a, b) => a - b);

  const entries: TimelineEntry[] = [];
  for (let index = 0; index < sortedBoundaries.length - 1; index += 1) {
    const sliceStart = sortedBoundaries[index];
    const sliceEnd = sortedBoundaries[index + 1];
    const midpoint = sliceStart + (sliceEnd - sliceStart) / 2;

    let winner: RawSegment | undefined;
    for (const segment of raw) {
      if (segment.start <= midpoint && segment.end > midpoint) {
        if (
          winner === undefined ||
          segment.precedence > winner.precedence ||
          (segment.precedence === winner.precedence && segment.source < winner.source)
        ) {
          winner = segment;
        }
      }
    }

    const previous = entries[entries.length - 1];
    if (winner === undefined) {
      if (previous !== null && previous !== undefined && previous.user === null) {
        previous.end = sliceEnd;
      } else {
        entries.push({ start: sliceStart, end: sliceEnd, user: null });
      }
    } else if (
      previous !== undefined &&
      previous.user === winner.user &&
      previous.end === sliceStart
    ) {
      previous.end = sliceEnd;
    } else {
      entries.push({
        start: sliceStart,
        end: sliceEnd,
        user: winner.user,
        source: winner.source,
      });
    }
  }

  return entries;
}

/** Covered (non-gap) segments only, already normalized and merged. */
export function coverageTimeline(
  schedule: Schedule,
  start: Instant,
  end: Instant,
  disambiguation: Disambiguation = "compatible",
): TimelineSegment[] {
  return buildTimeline(schedule, start, end, disambiguation).filter(
    (entry): entry is TimelineSegment => entry.user !== null,
  );
}

/** Winner at a single instant, or null when no layer covers it (restriction gap). */
export function onCallAt(
  schedule: Schedule,
  instant: Instant,
  disambiguation: Disambiguation = "compatible",
): UserId | null {
  const timeline = buildTimeline(schedule, instant, instant + 1, disambiguation);
  const entry = timeline[0];
  return entry === undefined || entry.user === null ? null : entry.user;
}

/** All distinct on-call users at `instant` across the given schedules. */
export function onCallUsers(
  schedules: Schedule[],
  instant: Instant,
  disambiguation: Disambiguation = "compatible",
): UserId[] {
  const users: UserId[] = [];
  const seen = new Set<UserId>();
  for (const schedule of schedules) {
    const user = onCallAt(schedule, instant, disambiguation);
    if (user !== null && !seen.has(user)) {
      seen.add(user);
      users.push(user);
    }
  }
  return users;
}
