/**
 * Symbolic 48-bit device-id interval algebra.
 *
 * Nothing in this module enumerates the identifier space.  Every operation is
 * linear in the number of interval boundaries, so a full-domain policy with a
 * handful of sparse exceptions is represented by a handful of ranges.
 */

export const DEVICE_ID_BITS = 48 as const;
export const DEVICE_ID_MAX = (1n << 48n) - 1n;
/** Exclusive end of the identifier domain. */
export const DEVICE_DOMAIN_END = 1n << 48n;

export interface Interval {
  readonly start: bigint;
  readonly end: bigint;
}

export type RangeSet = readonly Interval[];

export type ScalarDeviceId =
  | bigint
  | number
  | string
  | { id: ScalarDeviceId }
  | { deviceId: ScalarDeviceId }
  | { value: ScalarDeviceId };

export type IntervalInput =
  | ScalarDeviceId
  | readonly [ScalarDeviceId, ScalarDeviceId]
  | {
      start: ScalarDeviceId;
      end: ScalarDeviceId;
      exceptions?: RangeInput;
      except?: RangeInput;
    }
  | {
      lo: ScalarDeviceId;
      hi: ScalarDeviceId;
      exceptions?: RangeInput;
      except?: RangeInput;
    }
  | {
      from: ScalarDeviceId;
      to: ScalarDeviceId;
      exceptions?: RangeInput;
      except?: RangeInput;
    }
  | {
      min: ScalarDeviceId;
      max: ScalarDeviceId;
      exceptions?: RangeInput;
      except?: RangeInput;
    };

export type RangeInput =
  | IntervalInput
  | readonly IntervalInput[]
  | {
      ranges?: RangeInput;
      exceptions?: RangeInput;
      except?: RangeInput;
      start?: ScalarDeviceId;
      end?: ScalarDeviceId;
    };

const DECIMAL = /^\d+$/;
const HEX = /^0x[0-9a-f]+$/i;

function scalarOf(value: unknown): unknown {
  if (value !== null && typeof value === "object") {
    const object = value as Record<string, unknown>;
    if ("id" in object) return scalarOf(object.id);
    if ("deviceId" in object) return scalarOf(object.deviceId);
    if ("value" in object) return scalarOf(object.value);
  }
  return value;
}

export function toDeviceId(value: ScalarDeviceId): bigint {
  const raw = scalarOf(value);
  let result: bigint;
  if (typeof raw === "bigint") {
    result = raw;
  } else if (typeof raw === "number") {
    if (!Number.isInteger(raw)) {
      throw new RangeError(`device id must be an integer, got ${String(raw)}`);
    }
    result = BigInt(raw);
  } else if (typeof raw === "string") {
    const text = raw.trim();
    if (DECIMAL.test(text)) result = BigInt(text);
    else if (HEX.test(text)) result = BigInt(text);
    else throw new RangeError(`invalid device id ${JSON.stringify(raw)}`);
  } else {
    throw new TypeError(`invalid device id ${String(raw)}`);
  }
  if (result < 0n || result > DEVICE_ID_MAX) {
    throw new RangeError(`device id ${result.toString()} is outside the 48-bit domain`);
  }
  return result;
}

export function tryDeviceId(value: unknown): bigint | undefined {
  try {
    return toDeviceId(value as ScalarDeviceId);
  } catch {
    return undefined;
  }
}

export function formatDeviceId(value: bigint): string {
  return value.toString(10);
}

function looksLikeIntervalTuple(value: readonly unknown[]): boolean {
  if (value.length !== 2) return false;
  try {
    toDeviceId(value[0] as ScalarDeviceId);
    toDeviceId(value[1] as ScalarDeviceId);
    return true;
  } catch {
    return false;
  }
}

export function toInterval(input: IntervalInput): Interval {
  if (
    typeof input === "bigint" ||
    typeof input === "number" ||
    typeof input === "string" ||
    (input !== null &&
      typeof input === "object" &&
      ("id" in input || "deviceId" in input || "value" in input))
  ) {
    const id = toDeviceId(input as ScalarDeviceId);
    return { start: id, end: id };
  }

  if (Array.isArray(input)) {
    if (!looksLikeIntervalTuple(input)) {
      throw new TypeError("an interval tuple must contain two valid device ids");
    }
    const start = toDeviceId(input[0] as ScalarDeviceId);
    const end = toDeviceId(input[1] as ScalarDeviceId);
    if (start > end) throw new RangeError(`interval start ${start} exceeds end ${end}`);
    return { start, end };
  }

  if (input !== null && typeof input === "object") {
    const object = input as Record<string, unknown>;
    let startValue: unknown;
    let endValue: unknown;
    if ("start" in object && "end" in object) {
      startValue = object.start;
      endValue = object.end;
    } else if ("lo" in object && "hi" in object) {
      startValue = object.lo;
      endValue = object.hi;
    } else if ("from" in object && "to" in object) {
      startValue = object.from;
      endValue = object.to;
    } else if ("min" in object && "max" in object) {
      startValue = object.min;
      endValue = object.max;
    }
    if (startValue !== undefined && endValue !== undefined) {
      const start = toDeviceId(startValue as ScalarDeviceId);
      const end = toDeviceId(endValue as ScalarDeviceId);
      if (start > end) throw new RangeError(`interval start ${start} exceeds end ${end}`);
      return { start, end };
    }
  }

  throw new TypeError(`invalid interval ${JSON.stringify(input)}`);
}

function mergeRanges(ranges: readonly Interval[]): RangeSet {
  if (ranges.length < 2) return ranges;
  const sorted = [...ranges].sort((a, b) =>
    a.start < b.start ? -1 : a.start > b.start ? 1 : a.end < b.end ? -1 : a.end > b.end ? 1 : 0,
  );
  const merged: Interval[] = [];
  for (const range of sorted) {
    const previous = merged[merged.length - 1];
    if (previous !== undefined && range.start <= previous.end + 1n) {
      if (range.end > previous.end) {
        merged[merged.length - 1] = { start: previous.start, end: range.end };
      }
    } else {
      merged.push({ start: range.start, end: range.end });
    }
  }
  return merged;
}

function collectIntervals(input: RangeInput): RangeSet {
  if (input === undefined || input === null) return [];

  if (Array.isArray(input)) {
    if (looksLikeIntervalTuple(input)) {
      return [toInterval(input as unknown as IntervalInput)];
    }
    const result: Interval[] = [];
    for (const item of input) {
      if (item === undefined || item === null) continue;
      if (Array.isArray(item) && !looksLikeIntervalTuple(item)) {
        for (const nested of item) {
          if (nested !== undefined && nested !== null) result.push(toInterval(nested as IntervalInput));
        }
      } else {
        result.push(toInterval(item as IntervalInput));
      }
    }
    return result;
  }

  if (typeof input === "object") {
    const object = input as Record<string, unknown>;
    if ("ranges" in object) {
      const ranges = collectIntervals(object.ranges as RangeInput);
      const exceptions = object.exceptions ?? object.except;
      return exceptions === undefined
        ? ranges
        : rangeSubtract(ranges, normalizeRangeSet(exceptions as RangeInput));
    }
    if (
      "start" in object ||
      "lo" in object ||
      "from" in object ||
      "min" in object
    ) {
      const interval = toInterval(input as IntervalInput);
      const exceptions = object.exceptions ?? object.except;
      return exceptions === undefined
        ? [interval]
        : rangeSubtract([interval], normalizeRangeSet(exceptions as RangeInput));
    }
  }

  return [toInterval(input as IntervalInput)];
}

export function normalizeRangeSet(input?: RangeInput): RangeSet {
  return mergeRanges(collectIntervals(input as RangeInput));
}

export function emptyRange(): RangeSet {
  return [];
}

export function fullRange(): RangeSet {
  return [{ start: 0n, end: DEVICE_ID_MAX }];
}

export function rangeIsEmpty(range: RangeSet): boolean {
  return range.length === 0;
}

export function rangeContains(range: RangeSet, id: bigint): boolean {
  let low = 0;
  let high = range.length - 1;
  while (low <= high) {
    const middle = (low + high) >>> 1;
    const interval = range[middle]!;
    if (id < interval.start) high = middle - 1;
    else if (id > interval.end) low = middle + 1;
    else return true;
  }
  return false;
}

export function rangeSize(range: RangeSet): bigint {
  let total = 0n;
  for (const interval of range) total += interval.end - interval.start + 1n;
  return total;
}

export function rangeUnion(...ranges: readonly RangeSet[]): RangeSet {
  const all: Interval[] = [];
  for (const range of ranges) all.push(...range);
  return mergeRanges(all);
}

export function rangeIntersect(left: RangeSet, right: RangeSet): RangeSet {
  const result: Interval[] = [];
  let i = 0;
  let j = 0;
  while (i < left.length && j < right.length) {
    const a = left[i]!;
    const b = right[j]!;
    const start = a.start > b.start ? a.start : b.start;
    const end = a.end < b.end ? a.end : b.end;
    if (start <= end) result.push({ start, end });
    if (a.end < b.end) i += 1;
    else j += 1;
  }
  return result;
}

export function rangeSubtract(left: RangeSet, right: RangeSet): RangeSet {
  const result: Interval[] = [];
  let j = 0;
  for (const interval of left) {
    let cursor = interval.start;
    while (j < right.length && right[j]!.end < cursor) j += 1;
    let k = j;
    while (k < right.length && right[k]!.start <= interval.end) {
      const cut = right[k]!;
      if (cut.start > cursor) {
        result.push({ start: cursor, end: (cut.start < interval.end ? cut.start : interval.end) - 1n });
      }
      if (cut.end + 1n > cursor) cursor = cut.end + 1n;
      if (cursor > interval.end) break;
      k += 1;
    }
    if (cursor <= interval.end) result.push({ start: cursor, end: interval.end });
  }
  return mergeRanges(result);
}

export function rangeComplement(range: RangeSet): RangeSet {
  return rangeSubtract(fullRange(), range);
}

export function rangeIsSubset(subset: RangeSet, superset: RangeSet): boolean {
  return rangeIsEmpty(rangeSubtract(subset, superset));
}

export function rangeIntersects(left: RangeSet, right: RangeSet): boolean {
  return rangeIntersect(left, right).length > 0;
}

export function rangeEquals(left: RangeSet, right: RangeSet): boolean {
  if (left.length !== right.length) return false;
  for (let index = 0; index < left.length; index += 1) {
    if (left[index]!.start !== right[index]!.start || left[index]!.end !== right[index]!.end) {
      return false;
    }
  }
  return true;
}

export function rangeSignature(range: RangeSet): string {
  if (range.length === 0) return "empty";
  return range.map((interval) => `${interval.start}-${interval.end}`).join(",");
}

export function rangeToJSON(range: RangeSet): [string, string][] {
  return range.map((interval) => [interval.start.toString(), interval.end.toString()]);
}

export function rangeFromJSON(value: unknown): RangeSet {
  if (!Array.isArray(value)) throw new TypeError("range JSON must be an array");
  return mergeRanges(
    value.map((item) => {
      if (!Array.isArray(item) || item.length !== 2) {
        throw new TypeError("range JSON entries must be [start, end]");
      }
      return { start: toDeviceId(item[0] as ScalarDeviceId), end: toDeviceId(item[1] as ScalarDeviceId) };
    }),
  );
}

/** Add both sides of every half-open boundary to a sorted set. */
export function collectBoundaries(ranges: readonly RangeSet[], points: readonly bigint[] = []): bigint[] {
  const boundaries = new Set<bigint>([0n, DEVICE_DOMAIN_END]);
  const add = (value: bigint): void => {
    if (value >= 0n && value <= DEVICE_DOMAIN_END) boundaries.add(value);
  };
  for (const range of ranges) {
    for (const interval of range) {
      add(interval.start);
      add(interval.end + 1n);
    }
  }
  for (const point of points) {
    add(point);
    add(point + 1n);
  }
  return [...boundaries].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

export interface DomainCell {
  readonly start: bigint;
  readonly end: bigint;
}

export function cellsFromBoundaries(boundaries: readonly bigint[]): DomainCell[] {
  if (boundaries.length < 2) return [];
  const cells: DomainCell[] = [];
  for (let index = 0; index + 1 < boundaries.length; index += 1) {
    const start = boundaries[index]!;
    const next = boundaries[index + 1]!;
    if (start < next) cells.push({ start, end: next - 1n });
  }
  return cells;
}

export function assertValidRange(range: RangeSet): void {
  let previousEnd: bigint | undefined;
  for (const interval of range) {
    if (interval.start < 0n || interval.end > DEVICE_ID_MAX || interval.start > interval.end) {
      throw new RangeError(`invalid interval ${interval.start}-${interval.end}`);
    }
    if (previousEnd !== undefined && interval.start <= previousEnd + 1n) {
      throw new RangeError("range is not canonical");
    }
    previousEnd = interval.end;
  }
}
