export { LockManager } from './lockManager.ts';
export { ShrinkPhaseManager } from './monitor.ts';
export { ScheduleHistory } from './schedule.ts';
export { DataStore } from './store.ts';
export { TransactionManager } from './transactionManager.ts';
export type { Transaction } from './transactionManager.ts';
export {
  DeadlockError,
  ShrinkPhaseViolation,
  TransactionStateError,
} from './types.ts';
export type { LockMode, TxState } from './types.ts';
