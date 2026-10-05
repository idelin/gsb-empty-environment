/**
 * Explicit strategy for handling a replica whose version is
 * behind the session watermark.
 */
export enum StalePolicy {
  /** Keep polling the same node until it catches up or the deadline hits. */
  WAIT = "wait",
  /** Try the next node; only fail once every node is stale or time is up. */
  SWITCH = "switch",
  /** Do not wait or switch: fail immediately with a timeout error. */
  TIMEOUT = "timeout",
}

export type SleepFn = (ms: number) => Promise<void>;

export interface RouterOptions {
  policy: StalePolicy;
  /** Overall deadline for a single routed read, in milliseconds. */
  timeoutMs: number;
  /** Poll interval used by the WAIT policy. Defaults to 10ms. */
  pollIntervalMs?: number;
  /** Injectable sleep for deterministic tests. */
  sleep?: SleepFn;
}

export const defaultSleep: SleepFn = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));
