import { CumulativeToDeltaProcessor } from './cumulative-to-delta.js';
import { DeltaToCumulativeProcessor } from './delta-to-cumulative.js';

export { CumulativeToDeltaProcessor } from './cumulative-to-delta.js';
export { DeltaToCumulativeProcessor } from './delta-to-cumulative.js';
export { LruCache } from './lru.js';
export { seriesKey } from './series.js';
export type {
  Attributes,
  AttributeValue,
  ConversionMetadata,
  DataPoint,
  DropInfo,
  HistogramDataPoint,
  InstrumentType,
  Metric,
  NumberDataPoint,
  OutputDataPoint,
  OutputHistogramDataPoint,
  OutputMetric,
  OutputNumberDataPoint,
  ProcessorOptions,
  ProcessorStats,
  ResetReason,
  Temporality,
  TimeUnixNano,
} from './types.js';
export { createEmptyStats } from './types.js';
export { boundsEqual, isHistogram, validateHistogram, validateMetric } from './validate.js';

export type TemporalityProcessor =
  | CumulativeToDeltaProcessor
  | DeltaToCumulativeProcessor;

/**
 * Convenience factory selecting the processor for the desired output
 * temporality. Input temporality is always the opposite of the output.
 */
export function createTemporalityProcessor(
  outputTemporality: 'CUMULATIVE' | 'DELTA',
  options: ConstructorParameters<typeof CumulativeToDeltaProcessor>[0] = {},
): TemporalityProcessor {
  return outputTemporality === 'CUMULATIVE'
    ? new DeltaToCumulativeProcessor(options)
    : new CumulativeToDeltaProcessor(options);
}
