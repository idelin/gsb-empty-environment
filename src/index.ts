export type {
  ColumnBucket,
  DataPoint,
  IndexRange,
  MaybeValue,
  SampledPoint,
  SampleRole,
  Segment,
} from './types.ts';
export { computeSegments, isMissing } from './segments.ts';
export { bucketColumn, buildBuckets, mergeBuckets } from './buckets.ts';
export {
  assembleSegment,
  downsample,
  resolveColumnWidth,
  visibleColumnRange,
} from './downsample.ts';
export type { DownsampleParams } from './downsample.ts';
export {
  splitIntoRuns,
  verifyEnvelope,
  verifyNoGapCrossing,
} from './verify.ts';
export type { EnvelopeOptions, EnvelopeViolation } from './verify.ts';
export { DownsampleEngine } from './engine.ts';
export type { EngineOptions, ScheduledResult, Viewport } from './engine.ts';
