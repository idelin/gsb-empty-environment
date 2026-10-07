export { TransitionTable, InvalidTransitionTableError, MAX_OFFSET_MINUTES } from './table';
export type { Transition, TransitionTableInput } from './table';
export {
  localToUtc,
  utcToLocal,
  resolveLocal,
  NonexistentLocalTimeError,
  AmbiguousLocalTimeError,
} from './convert';
export type { LocalToUtcResult, Disambiguation } from './convert';
export { resolveInterval, NonPositiveIntervalError } from './interval';
export type { IntervalEndpointChoice, ResolvedInterval } from './interval';
export { enumerateUtcMapping } from './enumerate';
export type { UtcMapping } from './enumerate';

/** Wall-clock reading → local epoch milliseconds (timezone-independent). */
export function civilMs(
  year: number,
  month: number,
  day: number,
  hour = 0,
  minute = 0,
  second = 0,
  ms = 0,
): number {
  return Date.UTC(year, month - 1, day, hour, minute, second, ms);
}
