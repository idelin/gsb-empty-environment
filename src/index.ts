export { History } from './history.ts';
export { referenceMapRange } from './reference.ts';
export { captureContext } from './diagnostic.ts';
export {
  normalizeLineEndings,
  normalizeLineEndingsWithMap,
  similarityRelocate,
} from './similarity.ts';
export {
  assertUtf16Boundary,
  isHighSurrogate,
  isLowSurrogate,
  snapBackward,
  snapForward,
  splitsSurrogatePair,
} from './utf16.ts';
export { applyEditToText, transformRange, transformRangeBackward } from './edits.ts';
export type {
  Candidate,
  ContextSummary,
  Diagnostic,
  Edit,
  Evidence,
  ExcludedCandidate,
  Range,
  Relocation,
} from './types.ts';
export type { MapResult } from './history.ts';
