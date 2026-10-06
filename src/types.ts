export type MaybeValue = number | null | undefined;

export interface DataPoint {
  time: number;
  /** null / undefined / NaN means "missing" and splits the series into segments. */
  value: MaybeValue;
}

/** A maximal run of consecutive valid (non-missing) points. */
export interface Segment {
  id: number;
  /** Inclusive index into the original points array. */
  startIndex: number;
  /** Exclusive index into the original points array. */
  endIndex: number;
}

export type SampleRole =
  | 'first'        // first visible point of a segment
  | 'last'         // last visible point of a segment
  | 'min'          // minimum value of a pixel column
  | 'max'          // maximum value of a pixel column
  | 'edge-before'  // neighbor just before the viewport (same segment)
  | 'edge-after';  // neighbor just after the viewport (same segment)

/** Original-index range a sampled point stands for (its "evidence"). */
export interface IndexRange {
  fromIndex: number;
  toIndex: number;
}

export interface SampledPoint {
  /** Index into the original points array. */
  index: number;
  time: number;
  value: number;
  /** Identifies the segment; renderers must break polylines when it changes. */
  segmentId: number;
  /** Pixel column this point was bucketed into (edge neighbors keep their real column). */
  column: number;
  roles: SampleRole[];
  evidence: IndexRange;
}

/** Min/max/first/last summary of one pixel column inside one segment. */
export interface ColumnBucket {
  column: number;
  minIndex: number;
  minValue: number;
  maxIndex: number;
  maxValue: number;
  firstIndex: number;
  lastIndex: number;
}
