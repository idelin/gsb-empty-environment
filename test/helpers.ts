import { resolveWall, type Instant, type WallClock } from "../src/time.js";

export const HOUR = 3_600_000;
export const MINUTE = 60_000;

export function wall(
  timeZone: string,
  year: number,
  month: number,
  day: number,
  hour = 0,
  minute = 0,
): Instant {
  const clock: WallClock = {
    year,
    month,
    day,
    hour,
    minute,
    second: 0,
    millisecond: 0,
  };
  return resolveWall(timeZone, clock).instant;
}

export function iso(instant: Instant): string {
  return new Date(instant).toISOString();
}
