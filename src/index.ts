export {
  covers,
  merge,
  mergeDot,
  dominates,
  compare,
  dotKey,
  compareDots,
  type Dot,
  type VersionVector,
  type Causality,
} from './version-vector.ts';
export {
  emptyState,
  isCovered,
  applyEvent,
  reconcile,
  dedupValues,
  versionGraphSize,
  stableStringify,
  type DottedEvent,
  type DottedValue,
  type KeyState,
} from './dvv.ts';
export { Replica, type ReadResult } from './replica.ts';
