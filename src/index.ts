export type {
  DanglingReferenceInfo,
  EditResult,
  FixRange,
  Heading,
  LevelViolation,
  NumberChange,
  Reference,
  ResolvedReference,
  VersionedHeadingView,
} from './types.ts';
export { computeNumbering, subtreeEnd } from './numbering.ts';
export type { NumberingResult } from './numbering.ts';
export { LevelJumpError, OutlineStore } from './store.ts';
export type { ApplyOptions, Edit, InsertPosition, MoveTarget } from './store.ts';
export { formatReferenceDisplay } from './display.ts';
