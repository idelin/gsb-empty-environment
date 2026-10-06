export { stackSeries, seriesFromRecord, compareTimes } from './stack.ts';
export {
  approxEqual,
  checkAllZeroColumn,
  checkHiddenEquivalence,
  checkOrderInvariance,
  checkPercentConservation,
  normalizeResult,
  segmentHeights,
  EPS,
} from './conservation.ts';
export type { OrderInvarianceReport } from './conservation.ts';
export { RevisionStacker } from './revisions.ts';
export type { BatchRecord, PublishedStack } from './revisions.ts';
export { matrixPublish } from './matrixModel.ts';
export type { MatrixInput, MatrixPublication } from './matrixModel.ts';
export type {
  DataPoint,
  SeriesInput,
  StackedColumn,
  StackedPoint,
  StackMode,
  StackOptions,
  StackResult,
  TimeKey,
} from './types.ts';
