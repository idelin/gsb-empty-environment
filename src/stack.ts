import type {
  DataPoint,
  SeriesInput,
  StackedColumn,
  StackedPoint,
  StackOptions,
  StackResult,
  TimeKey,
} from './types.ts';

export function compareTimes(a: TimeKey, b: TimeKey): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  const sa = String(a);
  const sb = String(b);
  return sa < sb ? -1 : sa > sb ? 1 : 0;
}

/**
 * Aligns series on the sorted union of their timestamps and stacks values
 * per column. Positive values accumulate upward from zero, negative values
 * accumulate downward from zero, each in input (visible) series order.
 *
 * Missing points (absent or `null`) occupy no layer and do not move the
 * cursors; they are emitted with `missing: true` and a zero-height layer at
 * the baseline. Zero values are real layers of height zero at the current
 * cursor.
 *
 * In `percent` mode positive values are normalized by the column's positive
 * sum and negative values by the column's absolute negative sum, so each
 * side independently spans [0, 1] / [-1, 0] when its total is non-zero.
 */
export function stackSeries(
  seriesList: readonly SeriesInput[],
  options: StackOptions,
): StackResult {
  const hidden = options.hidden ?? new Set<string>();
  const visible = seriesList.filter((s) => !hidden.has(s.id));

  const lookups = new Map<string, Map<TimeKey, number | null>>();
  const timeSet = new Set<TimeKey>();
  for (const series of visible) {
    const byTime = new Map<TimeKey, number | null>();
    for (const point of series.points) {
      byTime.set(point.time, point.value);
      timeSet.add(point.time);
    }
    lookups.set(series.id, byTime);
  }
  const times = [...timeSet].sort(compareTimes);

  const columns: StackedColumn[] = times.map((time) => {
    let posTotal = 0;
    let negTotal = 0;
    for (const series of visible) {
      const raw = lookups.get(series.id)!.get(time);
      if (raw === undefined || raw === null) continue;
      if (raw > 0) posTotal += raw;
      else if (raw < 0) negTotal += -raw;
    }

    let posCursor = 0;
    let negCursor = 0;
    const points: StackedPoint[] = visible.map((series) => {
      const raw = lookups.get(series.id)!.get(time);
      if (raw === undefined || raw === null) {
        return { time, seriesId: series.id, value: null, missing: true, lower: 0, upper: 0 };
      }
      let height = raw;
      if (options.mode === 'percent') {
        if (raw > 0) height = posTotal > 0 ? raw / posTotal : 0;
        else if (raw < 0) height = negTotal > 0 ? raw / negTotal : 0;
        else height = 0;
      }
      let lower: number;
      let upper: number;
      if (height >= 0) {
        lower = posCursor;
        upper = posCursor + height;
        posCursor = upper;
      } else {
        upper = negCursor;
        lower = negCursor + height;
        negCursor = lower;
      }
      return { time, seriesId: series.id, value: raw, missing: false, lower, upper };
    });

    return {
      time,
      points,
      posTotal,
      negTotal,
      posExtent: posCursor,
      negExtent: negCursor,
    };
  });

  return {
    mode: options.mode,
    times,
    seriesIds: visible.map((s) => s.id),
    columns,
  };
}

/** Convenience helper to build a SeriesInput from a time->value record. */
export function seriesFromRecord(
  id: string,
  record: Readonly<Record<string, number | null>>,
): SeriesInput {
  const points: DataPoint[] = Object.entries(record).map(([time, value]) => ({
    time,
    value,
  }));
  return { id, points };
}
