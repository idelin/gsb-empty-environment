export type {
  AliasOp,
  AliasRef,
  Hop,
  ObjectRef,
  Resolution,
  Snapshot,
  Target,
} from './types.ts';
export { aliasKey, objectKey, targetEquals, targetKey } from './types.ts';
export { AliasStore } from './store.ts';
export type { CommitRecord } from './store.ts';
export { AliasResolver, DEFAULT_MAX_HOPS, traverse } from './resolver.ts';
export type { ResolveOptions } from './resolver.ts';
export { referenceResolve } from './reference.ts';
