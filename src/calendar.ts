import {
  Instance,
  WeeklyRule,
  generateDates,
  instanceId,
  isOccurrence,
  validateRule,
} from "./recurrence";

export interface PageRequest {
  /** Window start ordinal (inclusive). */
  from: number;
  /** Window end ordinal (exclusive). Must be finite. */
  to: number;
  /** Max items per page. Defaults to unbounded. */
  limit?: number;
  /** Opaque cursor from a previous page of the same window. */
  cursor?: string;
}

export interface Page {
  items: Instance[];
  /** Pass as `cursor` for the next page; null when the window is exhausted. */
  nextCursor: string | null;
}

interface RescheduleException {
  originalDate: number;
  newDate: number;
}

function compareInstances(a: Instance, b: Instance): number {
  if (a.date !== b.date) return a.date - b.date;
  if (a.originalDate !== b.originalDate) return a.originalDate - b.originalDate;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

function encodeCursor(item: Instance): string {
  return `${item.date}|${item.originalDate}|${item.id}`;
}

function decodeCursor(cursor: string): Instance {
  const parts = cursor.split("|");
  if (parts.length !== 3) throw new Error(`malformed cursor: ${cursor}`);
  const date = Number(parts[0]);
  const originalDate = Number(parts[1]);
  const id = parts[2];
  if (!Number.isInteger(date) || !Number.isInteger(originalDate) || id.length === 0) {
    throw new Error(`malformed cursor: ${cursor}`);
  }
  return { id, date, originalDate };
}

export class Calendar {
  private readonly rule: WeeklyRule;
  private readonly exceptions = new Map<string, RescheduleException>();

  constructor(rule: WeeklyRule) {
    validateRule(rule);
    this.rule = {
      ...rule,
      weekdays: Array.from(new Set(rule.weekdays)),
      exdates: rule.exdates ? [...rule.exdates] : undefined,
    };
  }

  /**
   * Moves an occurrence to `newDate`. The moved instance keeps its original
   * id and originalDate; it may share its new date with another instance.
   */
  reschedule(id: string, newDate: number): Instance {
    if (!Number.isInteger(newDate)) throw new Error("newDate must be an integer ordinal");
    const originalDate = this.requireOriginalDate(id);
    this.exceptions.set(id, { originalDate, newDate });
    return { id, date: newDate, originalDate };
  }

  /** Removes a previous reschedule, restoring the original date. */
  unreschedule(id: string): void {
    this.exceptions.delete(id);
  }

  /** Paginated expansion of [from, to), ordered by (date, originalDate, id). */
  expand(req: PageRequest): Page {
    const { from, to } = req;
    if (!Number.isFinite(to)) throw new Error("to must be finite");
    const limit = req.limit ?? Number.MAX_SAFE_INTEGER;
    if (limit < 1) throw new Error("limit must be >= 1");

    const byId = new Map<string, Instance>();
    for (const date of generateDates(this.rule, from, to)) {
      const id = instanceId(date);
      if (this.exceptions.has(id)) continue; // moved away from its slot
      byId.set(id, { id, date, originalDate: date });
    }
    for (const [id, ex] of this.exceptions) {
      if (ex.newDate >= from && ex.newDate < to) {
        byId.set(id, { id, date: ex.newDate, originalDate: ex.originalDate });
      }
    }

    const sorted = Array.from(byId.values()).sort(compareInstances);
    let offset = 0;
    if (req.cursor !== undefined) {
      const pivot = decodeCursor(req.cursor);
      offset = sorted.findIndex((it) => compareInstances(it, pivot) > 0);
      if (offset === -1) offset = sorted.length;
    }

    const items = sorted.slice(offset, offset + limit);
    const exhausted = offset + limit >= sorted.length;
    const nextCursor =
      !exhausted && items.length > 0 ? encodeCursor(items[items.length - 1]) : null;
    return { items, nextCursor };
  }

  /** All instances in [from, to), unpaginated. */
  expandAll(from: number, to: number): Instance[] {
    return this.expand({ from, to }).items;
  }

  private requireOriginalDate(id: string): number {
    const prefix = "inst@";
    if (!id.startsWith(prefix)) throw new Error(`unknown instance id: ${id}`);
    const originalDate = Number(id.slice(prefix.length));
    if (!Number.isInteger(originalDate) || !isOccurrence(this.rule, originalDate)) {
      throw new Error(`unknown instance id: ${id}`);
    }
    return originalDate;
  }
}
