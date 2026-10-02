/**
 * 协程指令集：Fiber 的函数体是一个 Generator，每 yield 一条指令，
 * 由调度器解释执行。指令即调度器与协程之间的"控制反转"边界。
 */

export type Instruction =
  | { op: 'nop' }
  | { op: 'sleep'; ticks: number }
  | { op: 'lock'; name: string }
  | { op: 'unlock'; name: string }
  | { op: 'send'; channel: string; value?: unknown }
  | { op: 'recv'; channel: string }
  | { op: 'require'; resource: string }
  | { op: 'provide'; resource: string; value?: unknown }
  | { op: 'spawn'; name: string; body: FiberBody };

export type FiberBody = () => Generator<Instruction, void, unknown>;

export const nop = (): Instruction => ({ op: 'nop' });
export const sleep = (ticks: number): Instruction => ({ op: 'sleep', ticks });
export const lock = (name: string): Instruction => ({ op: 'lock', name });
export const unlock = (name: string): Instruction => ({ op: 'unlock', name });
export const send = (channel: string, value?: unknown): Instruction => ({ op: 'send', channel, value });
export const recv = (channel: string): Instruction => ({ op: 'recv', channel });
export const requireResource = (resource: string): Instruction => ({ op: 'require', resource });
export const provideResource = (resource: string, value?: unknown): Instruction => ({ op: 'provide', resource, value });
export const spawn = (name: string, body: FiberBody): Instruction => ({ op: 'spawn', name, body });
