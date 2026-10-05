export type {
  CompareOptions,
  DiffReport,
  Divergence,
  DivergenceKind,
  Segment,
  SideStats,
  TraceEvent,
  TraceInput,
  ValueDiff,
  WindowSide,
} from './types.js';
export { diffTrajectories, normalizeSegments } from './diff.js';
export { deepDiff, eventView } from './compare.js';
export { SegmentCursor } from './cursor.js';
