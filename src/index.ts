export { VersionVector } from './version-vector.ts';
export type { Ordering, ReplicaId, VectorEntries } from './version-vector.ts';
export { Replica } from './replica.ts';
export type { GetResult, PutOutcome, PutResult, VersionedValue } from './replica.ts';
export { mergeSiblings, syncBidirectional, syncOneWay } from './synchronizer.ts';
export type { KeyMergeReport, MergeAction, SyncReport } from './synchronizer.ts';
