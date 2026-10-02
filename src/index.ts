export {
  CircularSignalUpdate,
  ReactiveLoopGuard,
  formatLoopReport,
} from './guard.ts';
export type {
  CausalAction,
  CausalEntry,
  LoopReason,
  LoopReport,
} from './guard.ts';
export { createReactiveSystem } from './reactive.ts';
export type {
  Computed,
  EffectHandle,
  ReactiveSystem,
  ReactiveSystemOptions,
  Signal,
} from './reactive.ts';
