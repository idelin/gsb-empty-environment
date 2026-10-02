/**
 * 协程指令（Effect）定义。
 * 每条指令对应 Fiber 生成器的一次 yield，即调试器可单步的“一条指令”。
 */
export type Effect =
  | { kind: 'sleep'; ticks: number }
  | { kind: 'lock.acquire'; lock: string }
  | { kind: 'lock.release'; lock: string }
  | { kind: 'chan.send'; channel: string; value: unknown }
  | { kind: 'chan.recv'; channel: string }
  | { kind: 'resource.wait'; resource: string }
  | { kind: 'resource.provide'; resource: string; value?: unknown };

export const sleep = (ticks: number): Effect => ({ kind: 'sleep', ticks });
export const acquireLock = (lock: string): Effect => ({ kind: 'lock.acquire', lock });
export const releaseLock = (lock: string): Effect => ({ kind: 'lock.release', lock });
export const chanSend = (channel: string, value: unknown): Effect => ({
  kind: 'chan.send',
  channel,
  value,
});
export const chanRecv = (channel: string): Effect => ({ kind: 'chan.recv', channel });
export const waitResource = (resource: string): Effect => ({
  kind: 'resource.wait',
  resource,
});
export const provideResource = (resource: string, value?: unknown): Effect => ({
  kind: 'resource.provide',
  resource,
  value,
});
