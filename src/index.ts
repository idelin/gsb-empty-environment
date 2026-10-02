export {
  AcquireAbortedError,
  LeaseLostError,
  NotLockHolderError,
  StaleFenceTokenError,
} from "./errors.ts";
export { LockServer } from "./lock-server.ts";
export type { LeaseSnapshot } from "./lock-server.ts";
export { LockClient, LockHandle } from "./lock-client.ts";
export type {
  AcquireOptions,
  LockHandleEvents,
  LockHandleState,
} from "./lock-client.ts";
export { FencedStore } from "./fenced-store.ts";
