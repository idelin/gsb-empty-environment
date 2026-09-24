export { FoldedDocument } from "./document.ts";
export type {
  EditResult,
  FoldedDocumentOptions,
} from "./document.ts";
export {
  buildFoldTree,
  contains,
  flattenFoldTree,
  intersects,
  normalizeFolds,
  overlapsPartially,
  validateFolds,
} from "./folds.ts";
export type { FoldNode, FoldRegion } from "./folds.ts";
export {
  isHighSurrogate,
  isLowSurrogate,
  isSafeOffset,
  offsetAt,
  positionAt,
  snapOffset,
  snapRangeOutward,
} from "./utf16.ts";
export type { Position } from "./utf16.ts";
