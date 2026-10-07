export {
  RangeAffinity,
  isValidRange,
  mapPositionThroughEdit,
  mapPositionThroughEdits,
  mapRangeThroughEdit,
  mapRangeThroughEdits,
  range,
  rangesEqual,
} from './range.ts';
export type { Affinity, Edit, Range } from './range.ts';

export { VersionedDocument } from './document.ts';
export type { TransactionBuilder } from './document.ts';

export { applyCompletion, resolveCompletionRanges } from './completion.ts';
export type {
  ApplyCompletionFailure,
  ApplyCompletionResult,
  CompletionItem,
  ResolveCompletionResult,
} from './completion.ts';
