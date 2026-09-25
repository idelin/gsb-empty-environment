export { diffTrees } from './diff.ts';
export { applyPatch } from './apply.ts';
export { buildIndex } from './index-tree.ts';
export { jsonEqual, canonicalize, toPointer } from './json.ts';
export { TreeDiffError } from './types.ts';
export type {
  JsonObject,
  JsonValue,
  Operation,
  InsertOp,
  MoveOp,
  RemoveOp,
  UpdateSetOp,
  UpdateUnsetOp,
  PathSegment,
} from './types.ts';
