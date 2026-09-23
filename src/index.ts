export { ManualScheduler } from "./scheduler.ts";
export { OwnershipLedger } from "./ledger.ts";
export type { LedgerEntry, LedgerViolation } from "./ledger.ts";
export { Journal, replay, serializeError } from "./journal.ts";
export type { Op, ScopeState, ScopeRecord, NodeRecord, SerializedError } from "./journal.ts";
export { Scope, computeDisposalPlan } from "./scope.ts";
export type { AcquireHandle, AcquireOptions, Lease, DisposalPlan } from "./scope.ts";
export {
  ScopeDestroyedError,
  AlreadyCanceledError,
  DependencyCycleError,
  BorrowError,
} from "./errors.ts";
