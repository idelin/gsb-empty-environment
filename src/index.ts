export {
  OffsetError,
  computeLineStarts,
  isHighSurrogate,
  isLowSurrogate,
  offsetToPosition,
  positionToOffset,
  splitsSurrogatePair,
  type Position,
} from './offsets.ts';
export {
  EditConflictError,
  applyEdits,
  assertNoConflicts,
  findConflict,
  mapOffset,
  mapRange,
  type Association,
  type Edit,
  type Range,
} from './transaction.ts';
export {
  Editor,
  Transaction,
  type Annotation,
  type Selection,
  type UndoRecord,
} from './editor.ts';
