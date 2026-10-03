/**
 * Timezone-aware working calendar.
 *
 * The solver works in abstract "working minutes since project start". This
 * calendar projects those offsets onto wall-clock time: it knows the local
 * timezone (as a fixed UTC offset), which weekdays are worked, and the daily
 * working window, and skips non-working time when converting.
 */

export interface CalendarOptions {
  /** Fixed offset from UTC in minutes, e.g. 480 for UTC+8. */
  timeZoneOffsetMinutes: number;
  /** Working weekdays in local time (0 = Sunday ... 6 = Saturday). */
  workDays: number[];
  /** Start of the daily working window, minutes after local midnight. */
  dayStartMinutes: number;
  /** End of the daily working window, minutes after local midnight. */
  dayEndMinutes: number;
}

const MINUTE_MS = 60_000;
const DAY_MINUTES = 24 * 60;

export class Calendar {
  readonly options: CalendarOptions;
  private readonly workDaySet: Set<number>;

  constructor(options: CalendarOptions) {
    this.options = options;
    this.workDaySet = new Set(options.workDays);
  }

  /** Minutes of working capacity per working day. */
  get workMinutesPerDay(): number {
    return this.options.dayEndMinutes - this.options.dayStartMinutes;
  }

  /** Local civil time (minutes since epoch) for a UTC instant. */
  private localMinutes(utc: Date): number {
    return Math.floor(utc.getTime() / MINUTE_MS) + this.options.timeZoneOffsetMinutes;
  }

  private isWorkDay(localDay: number): boolean {
    // 1970-01-01 was a Thursday (weekday 4).
    const weekday = ((localDay % 7) + 7 + 4) % 7;
    return this.workDaySet.has(weekday);
  }

  isWorkingMoment(utc: Date): boolean {
    const local = this.localMinutes(utc);
    const day = Math.floor(local / DAY_MINUTES);
    const minuteOfDay = local - day * DAY_MINUTES;
    return (
      this.isWorkDay(day) &&
      minuteOfDay >= this.options.dayStartMinutes &&
      minuteOfDay < this.options.dayEndMinutes
    );
  }

  private toUtc(localMinutes: number): Date {
    return new Date((localMinutes - this.options.timeZoneOffsetMinutes) * MINUTE_MS);
  }

  /** Next working-period start at or after the given local minute. */
  private nextPeriodStart(local: number): number {
    let day = Math.floor(local / DAY_MINUTES);
    const minuteOfDay = local - day * DAY_MINUTES;
    if (this.isWorkDay(day) && minuteOfDay < this.options.dayStartMinutes) {
      return day * DAY_MINUTES + this.options.dayStartMinutes;
    }
    if (this.isWorkDay(day) && minuteOfDay < this.options.dayEndMinutes) {
      return local;
    }
    do {
      day += 1;
    } while (!this.isWorkDay(day));
    return day * DAY_MINUTES + this.options.dayStartMinutes;
  }

  /** Add `workMinutes` of working time to a UTC instant. */
  addWorkTime(start: Date, workMinutes: number): Date {
    let local = this.nextPeriodStart(this.localMinutes(start));
    let remaining = workMinutes;
    while (remaining > 0) {
      const day = Math.floor(local / DAY_MINUTES);
      const periodEnd = day * DAY_MINUTES + this.options.dayEndMinutes;
      const available = periodEnd - local;
      if (remaining <= available) return this.toUtc(local + remaining);
      remaining -= available;
      local = this.nextPeriodStart(periodEnd + 1);
    }
    return this.toUtc(local);
  }

  /** Working minutes elapsed between two UTC instants (b must be >= a). */
  workMinutesBetween(a: Date, b: Date): number {
    let total = 0;
    let cursor = a;
    while (cursor.getTime() + MINUTE_MS <= b.getTime()) {
      if (this.isWorkingMoment(cursor)) total += 1;
      cursor = new Date(cursor.getTime() + MINUTE_MS);
    }
    return total;
  }

  /**
   * Project a solver offset (working minutes since project start) onto the
   * wall clock. The project start itself counts as offset 0.
   */
  toWallClock(workMinutesSinceProjectStart: number, projectStart: Date): Date {
    return this.addWorkTime(projectStart, workMinutesSinceProjectStart);
  }

  /** Format a UTC instant in the calendar's timezone: 2026-01-05T09:00:00+08:00 */
  format(utc: Date): string {
    const shifted = new Date(
      utc.getTime() + this.options.timeZoneOffsetMinutes * MINUTE_MS,
    );
    const pad = (v: number) => String(v).padStart(2, '0');
    const date =
      `${shifted.getUTCFullYear()}-${pad(shifted.getUTCMonth() + 1)}-${pad(shifted.getUTCDate())}` +
      `T${pad(shifted.getUTCHours())}:${pad(shifted.getUTCMinutes())}:${pad(shifted.getUTCSeconds())}`;
    const offset = this.options.timeZoneOffsetMinutes;
    const sign = offset >= 0 ? '+' : '-';
    const abs = Math.abs(offset);
    return `${date}${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`;
  }
}
