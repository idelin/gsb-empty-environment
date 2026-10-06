import { compareTimes, stackSeries } from './stack.ts';
import type { BatchRecord } from './revisions.ts';
import type {
  SeriesInput,
  StackMode,
  StackResult,
  TimeKey,
} from './types.ts';

export interface MatrixInput {
  mode: StackMode;
  /** Required series in stacking order; retracted series are simply absent. */
  required: readonly string[];
  hidden?: readonly string[];
  /** Every live batch, in any arrival order. */
  batches: readonly BatchRecord[];
}

export interface MatrixPublication {
  barriers: Map<TimeKey, number>;
  pendingTimes: TimeKey[];
  result: StackResult;
}

interface CellEntry {
  revision: number;
  value: number | null;
}

/**
 * Independent full-matrix reference model for the revision dataflow.
 *
 * Instead of per-series sorted batch lists it materializes the dense
 * series x time matrix, keeping every (revision, value) entry ever reported
 * for each cell, then derives each column's barrier by scanning the whole
 * column and picks each cell's value by scanning its full history for the
 * newest entry at or below the barrier. Used to cross-check RevisionStacker.
 */
export function matrixPublish(input: MatrixInput): MatrixPublication {
  const hidden = new Set(input.hidden ?? []);
  const ids = input.required.filter((id) => !hidden.has(id));
  const idSet = new Set(ids);

  const matrix = new Map<string, Map<TimeKey, CellEntry[]>>();
  const timeSet = new Set<TimeKey>();
  for (const batch of input.batches) {
    if (!idSet.has(batch.seriesId)) continue;
    let row = matrix.get(batch.seriesId);
    if (!row) {
      row = new Map();
      matrix.set(batch.seriesId, row);
    }
    for (const point of batch.points) {
      let cell = row.get(point.time);
      if (!cell) {
        cell = [];
        row.set(point.time, cell);
      }
      cell.push({ revision: batch.revision, value: point.value });
      timeSet.add(point.time);
    }
  }
  const times = [...timeSet].sort(compareTimes);

  const barriers = new Map<TimeKey, number>();
  const pendingTimes: TimeKey[] = [];
  const seriesInputs: SeriesInput[] = ids.map((id) => ({ id, points: [] }));

  for (const time of times) {
    let barrier = Number.POSITIVE_INFINITY;
    for (const id of ids) {
      const cell = matrix.get(id)?.get(time);
      if (!cell || cell.length === 0) {
        barrier = -1;
        break;
      }
      let latest = -1;
      for (const entry of cell) latest = Math.max(latest, entry.revision);
      barrier = Math.min(barrier, latest);
    }
    if (barrier < 0) {
      pendingTimes.push(time);
      continue;
    }
    barriers.set(time, barrier);
    for (let i = 0; i < ids.length; i += 1) {
      const cell = matrix.get(ids[i])?.get(time) ?? [];
      let best: CellEntry | undefined;
      for (const entry of cell) {
        if (entry.revision <= barrier && (!best || entry.revision > best.revision)) {
          best = entry;
        }
      }
      seriesInputs[i].points.push({
        time,
        value: best === undefined ? null : best.value,
      });
    }
  }

  return {
    barriers,
    pendingTimes,
    result: stackSeries(seriesInputs, { mode: input.mode }),
  };
}
