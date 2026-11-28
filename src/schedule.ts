/**
 * Schedule solver: layered rotations, restrictions and overrides into a
 * precise, non-overlapping UTC coverage timeline.
 *
 * Priority rule: Override > higher-priority layer > lower-priority layer.
 */

import type {
  CoverageSegment,
  Override,
  Restriction,
  RotationLayer,
  Schedule,
  UserId,
  WallTime,
} from "./types.js";
import {
  addWallDays,
  utcToZonedParts,
  zonedPartsToUtc,
  zonedTimeToUtc,
} from "./timezone.js";

const MS_PER_DAY = 86_400_000;

interface RawInterval {
  startMs: number;
  endMs: number;
  userId: UserId;
  source: CoverageSegment["source"];
  priority: number;
  rank: number;
}

interface LayerInterval {
  startMs: number;
  endMs: number;
  userId: UserId;
  source: Extract<CoverageSegment["source"], { type: "layer" }>;
}

/**
 * Build the coverage timeline over the half-open UTC window
 * [windowStartMs, windowEndMs). Adjacent same-user segments are coalesced.
 */
export function buildTimeline(
  schedule: Schedule,
  windowStartMs: number,
  windowEndMs: number,
): CoverageSegment[] {
  if (windowEndMs <= windowStartMs) return [];

  const intervals: RawInterval[] = [];

  const layers = [...schedule.layers].sort((a, b) => a.priority - b.priority);
  layers.forEach((layer, index) => {
    for (const segment of layerCoverage(layer, windowStartMs, windowEndMs)) {
      intervals.push({ ...segment, priority: layer.priority, rank: index });
    }
  });

  const overrides = [...(schedule.overrides ?? [])].sort(
    (a, b) => (a.priority ?? 0) - (b.priority ?? 0),
  );
  overrides.forEach((override: Override, index) => {
    const startMs = Math.max(override.startMs, windowStartMs);
    const endMs = Math.min(override.endMs, windowEndMs);
    if (endMs > startMs) {
      intervals.push({
        startMs,
        endMs,
        userId: override.userId,
        source: { type: "override", id: override.id },
        priority: Number.MAX_SAFE_INTEGER,
        rank: 1_000_000 + index,
      });
    }
  });

  return compose(intervals);
}

/** On-call user at a single UTC instant, or null if nobody is on duty. */
export function whoIsOnCall(
  schedule: Schedule,
  instantMs: number,
): UserId | null {
  const timeline = buildTimeline(schedule, instantMs, instantMs + 1);
  return timeline.length > 0 ? timeline[0].userId : null;
}

function layerCoverage(
  layer: RotationLayer,
  windowStartMs: number,
  windowEndMs: number,
): LayerInterval[] {
  let intervals: LayerInterval[] = generateShifts(
    layer,
    windowStartMs,
    windowEndMs,
  ).map(
    (shift) => ({
      startMs: shift.startMs,
      endMs: shift.endMs,
      userId: shift.userId,
      source: { type: "layer", id: layer.id } as const,
    }),
  );

  if (layer.restrictions && layer.restrictions.length > 0) {
    const masks = restrictionIntervals(
      layer.restrictions,
      layer.timeZone,
      windowStartMs,
      windowEndMs,
    );
    intervals = intersectAll(intervals, masks, layer.id);
  }
  return intervals;
}

interface Shift {
  startMs: number;
  endMs: number;
  userId: UserId;
}

type StepUnit = "day" | "week" | "custom";

/**
 * Enumerate rotation shifts intersecting the window. Handoff instants are
 * derived by stepping local wall time, so real spacing is 23h/25h on DST days.
 */
function generateShifts(
  layer: RotationLayer,
  windowStartMs: number,
  windowEndMs: number,
): Shift[] {
  if (layer.members.length === 0) return [];

  const anchorMs = zonedPartsToUtc(layer.timeZone, layer.anchorLocal);
  const stepUnit: StepUnit =
    layer.unit === "custom"
      ? "custom"
      : layer.unit === "weekly"
        ? "week"
        : "day";

  let index = estimateShiftIndex(layer, anchorMs, windowStartMs, stepUnit);

  // Walk back until this shift definitely contains the window start.
  let guard = 0;
  while (guard < 1_000_000) {
    const shiftStart = shiftStartForIndex(layer, anchorMs, index, stepUnit);
    if (shiftStart <= windowStartMs) break;
    index -= 1;
    guard += 1;
  }

  const result: Shift[] = [];
  guard = 0;
  while (guard < 1_000_000) {
    const shiftStart = shiftStartForIndex(layer, anchorMs, index, stepUnit);
    const nextStart = shiftStartForIndex(
      layer,
      anchorMs,
      index + 1,
      stepUnit,
    );
    if (shiftStart >= windowEndMs) break;
    const startMs = Math.max(shiftStart, windowStartMs);
    const endMs = Math.min(nextStart, windowEndMs);
    if (endMs > startMs) {
      result.push({
        startMs,
        endMs,
        userId: layer.members[mod(index, layer.members.length)],
      });
    }
    index += 1;
    guard += 1;
  }
  return result;
}

function shiftStartForIndex(
  layer: RotationLayer,
  anchorMs: number,
  index: number,
  stepUnit: StepUnit,
): number {
  if (index === 0) return anchorMs;
  if (stepUnit === "custom") {
    return anchorMs + index * (layer.customDurationMs ?? MS_PER_DAY);
  }
  const parts = utcToZonedParts(layer.timeZone, anchorMs);
  const dayIncrement = index * (stepUnit === "week" ? 7 : 1);
  const shifted = addWallDays(parts, dayIncrement);
  return zonedPartsToUtc(layer.timeZone, {
    year: shifted.year,
    month: shifted.month,
    day: shifted.day,
    hour: parts.hour,
    minute: parts.minute,
    second: parts.second,
    millisecond: parts.millisecond,
  });
}

/** Coarse starting index; the caller walks back to the exact containing shift. */
function estimateShiftIndex(
  layer: RotationLayer,
  anchorMs: number,
  targetMs: number,
  stepUnit: StepUnit,
): number {
  if (stepUnit === "custom") {
    return Math.floor(
      (targetMs - anchorMs) / (layer.customDurationMs ?? MS_PER_DAY),
    );
  }
  const anchorParts = utcToZonedParts(layer.timeZone, anchorMs);
  const targetParts = utcToZonedParts(layer.timeZone, targetMs);
  const dayDiff = daysBetween(
    anchorParts.year,
    anchorParts.month,
    anchorParts.day,
    targetParts.year,
    targetParts.month,
    targetParts.day,
  );
  const divisor = stepUnit === "week" ? 7 : 1;
  return Math.floor(dayDiff / divisor);
}

function daysBetween(
  y1: number,
  m1: number,
  d1: number,
  y2: number,
  m2: number,
  d2: number,
): number {
  const a = Date.UTC(y1, m1 - 1, d1);
  const b = Date.UTC(y2, m2 - 1, d2);
  return Math.round((b - a) / MS_PER_DAY);
}

/** UTC intervals covered by the union of restriction rules in the window. */
function restrictionIntervals(
  restrictions: Restriction[],
  timeZone: string,
  windowStartMs: number,
  windowEndMs: number,
): RawInterval[] {
  const pieces: RawInterval[] = [];
  for (const restriction of restrictions) {
    if (restriction.kind !== "weekly") continue;
    const weekdaySet = new Set(restriction.weekdays);
    const startParts = utcToZonedParts(
      timeZone,
      windowStartMs - MS_PER_DAY,
    );
    const endParts = utcToZonedParts(timeZone, windowEndMs + MS_PER_DAY);
    let dayParts = {
      year: startParts.year,
      month: startParts.month,
      day: startParts.day,
    };
    const endDayParts = {
      year: endParts.year,
      month: endParts.month,
      day: endParts.day,
    };
    let guard = 0;
    while (guard < 5000) {
      if (
        dayParts.year > endDayParts.year ||
        (dayParts.year === endDayParts.year &&
          compareMonthDay(dayParts, endDayParts) > 0)
      ) {
        break;
      }
      const weekday = new Date(
        Date.UTC(dayParts.year, dayParts.month - 1, dayParts.day),
      ).getUTCDay();
      if (weekdaySet.has(weekday)) {
        pushRestrictionWindow(
          pieces,
          timeZone,
          dayParts,
          restriction.start,
          restriction.end,
        );
      }
      dayParts = addWallDays(dayParts, 1);
      guard += 1;
    }
  }
  return mergeRaw(pieces);
}

function compareMonthDay(
  a: { year: number; month: number; day: number },
  b: { year: number; month: number; day: number },
): number {
  if (a.month !== b.month) return a.month - b.month;
  return a.day - b.day;
}

function pushRestrictionWindow(
  pieces: RawInterval[],
  timeZone: string,
  dateParts: { year: number; month: number; day: number },
  start: WallTime,
  end: WallTime,
): void {
  const startMinutes = start.hour * 60 + start.minute;
  const endMinutes = end.hour * 60 + end.minute;
  const startMs = zonedTimeToUtc(timeZone, dateParts, start);

  if (endMinutes > startMinutes) {
    const endMs = zonedTimeToUtc(timeZone, dateParts, end);
    pieces.push(dummyInterval(startMs, endMs));
    return;
  }

  // Cross-midnight window: split into evening and morning UTC pieces.
  const nextDate = addWallDays(dateParts, 1);
  const nextMidnightMs = zonedTimeToUtc(timeZone, nextDate, {
    hour: 0,
    minute: 0,
  });
  // Evening piece: start wall time -> next local midnight.
  pieces.push(dummyInterval(startMs, nextMidnightMs));
  // Morning piece: next local midnight -> end wall time on the next day.
  const morningEndMs = zonedTimeToUtc(timeZone, nextDate, end);
  pieces.push(dummyInterval(nextMidnightMs, morningEndMs));
}

function dummyInterval(startMs: number, endMs: number): RawInterval {
  return {
    startMs,
    endMs,
    userId: "",
    source: { type: "layer", id: "restriction" },
    priority: 0,
    rank: 0,
  };
}

function intersectAll(
  coverage: LayerInterval[],
  masks: RawInterval[],
  layerId: string,
): LayerInterval[] {
  const out: LayerInterval[] = [];
  for (const interval of coverage) {
    for (const mask of masks) {
      const startMs = Math.max(interval.startMs, mask.startMs);
      const endMs = Math.min(interval.endMs, mask.endMs);
      if (endMs > startMs) {
        out.push({
          startMs,
          endMs,
          userId: interval.userId,
          source: { type: "layer", id: layerId },
        });
      }
    }
  }
  return out;
}

function mergeRaw(intervals: RawInterval[]): RawInterval[] {
  if (intervals.length === 0) return [];
  const sorted = [...intervals].sort((a, b) => a.startMs - b.startMs);
  const merged: RawInterval[] = [];
  for (const interval of sorted) {
    const last = merged[merged.length - 1];
    if (last && interval.startMs <= last.endMs) {
      last.endMs = Math.max(last.endMs, interval.endMs);
    } else {
      merged.push({ ...interval });
    }
  }
  return merged;
}

/**
 * Sweep all interval boundaries. Within each elementary slice the highest
 * priority interval (then rank) wins; uncovered slices are dropped. Adjacent
 * slices sharing the same winning user are coalesced.
 */
function compose(intervals: RawInterval[]): CoverageSegment[] {
  if (intervals.length === 0) return [];

  const boundaries = new Set<number>();
  for (const interval of intervals) {
    boundaries.add(interval.startMs);
    boundaries.add(interval.endMs);
  }
  const sortedBoundaries = [...boundaries].sort((a, b) => a - b);

  const segments: CoverageSegment[] = [];
  for (let i = 0; i < sortedBoundaries.length - 1; i++) {
    const startMs = sortedBoundaries[i];
    const endMs = sortedBoundaries[i + 1];
    const winner = pickWinner(intervals, startMs);
    if (!winner) continue;

    const last = segments[segments.length - 1];
    if (
      last &&
      last.endMs === startMs &&
      last.userId === winner.userId
    ) {
      last.endMs = endMs;
    } else {
      segments.push({
        startMs,
        endMs,
        userId: winner.userId,
        source: winner.source,
      });
    }
  }
  return segments;
}

function pickWinner(
  intervals: RawInterval[],
  probeMs: number,
): RawInterval | null {
  let winner: RawInterval | null = null;
  for (const interval of intervals) {
    if (interval.startMs <= probeMs && interval.endMs > probeMs) {
      if (
        winner === null ||
        interval.priority > winner.priority ||
        (interval.priority === winner.priority && interval.rank > winner.rank)
      ) {
        winner = interval;
      }
    }
  }
  return winner;
}

function mod(value: number, divisor: number): number {
  return ((value % divisor) + divisor) % divisor;
}
