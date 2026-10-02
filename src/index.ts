export { WireError, type WireErrorCode } from './wire/errors.ts';
export {
  encodeVarint, decodeVarint, varintLength,
  zigzagEncode32, zigzagDecode32, zigzagEncode64, zigzagDecode64, toSigned64,
} from './wire/varint.ts';
export {
  WireReader,
  WIRE_VARINT, WIRE_I64, WIRE_LEN, WIRE_SGROUP, WIRE_EGROUP, WIRE_I32,
} from './wire/reader.ts';
export { WireWriter } from './wire/writer.ts';
export { decodeMessage, type DecodeOptions } from './proto/decoder.ts';
export { encodeMessage } from './proto/encoder.ts';
export type { FieldDescriptor, MessageDescriptor, FieldType } from './proto/schema.ts';
export { Simulator, StepLimitExceeded, type EventRecord, type RunResult } from './sim/simulator.ts';
export { SplitMix64 } from './sim/rng.ts';
export { MinHeap } from './sim/heap.ts';
export { TraceRecorder, diffTraces, type TraceEntry, type Divergence } from './sim/trace.ts';
export { runWorkload, type WorkloadResult, type WorkloadOptions } from './sim/workload.ts';
