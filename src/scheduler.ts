import { Fiber } from './fiber.ts';
import type { FiberState } from './fiber.ts';
import type { Instruction, FiberBody } from './instructions.ts';

export interface Transition {
  tick: number;
  fiberId: number;
  fiberName: string;
  from: FiberState;
  to: FiberState;
  pc: number;
  note: string;
}

type TransitionListener = (t: Transition) => void;
/** 调试器钩子：在指令执行前调用，返回 false 表示拦截（断点命中） */
type BeforeInstructionHook = (fiber: Fiber) => boolean;

interface Mutex {
  holder: Fiber | null;
  queue: Fiber[];
}

/** 会合（无缓冲）Channel */
interface Rendezvous {
  recvQueue: Fiber[];
  sendQueue: { fiber: Fiber; value: unknown }[];
}

interface Resource {
  initialized: boolean;
  value: unknown;
  waiters: Fiber[];
}

function describeInstruction(ins: Instruction): string {
  switch (ins.op) {
    case 'nop': return 'nop';
    case 'sleep': return `sleep(${ins.ticks})`;
    case 'lock': return `lock("${ins.name}")`;
    case 'unlock': return `unlock("${ins.name}")`;
    case 'send': return `send("${ins.channel}")`;
    case 'recv': return `recv("${ins.channel}")`;
    case 'require': return `require("${ins.resource}")`;
    case 'provide': return `provide("${ins.resource}")`;
    case 'spawn': return `spawn("${ins.name}")`;
  }
}

/**
 * 离散时间 Fiber 调度器。
 * 全局时钟以 tick 为单位，只有调用 tick() 才前进 —— 控制权完全在使用者手中。
 * 每个 tick 内，所有就绪协程各执行一条指令（确定性 FIFO 顺序）。
 */
export class Scheduler {
  tick = 0;
  clockPaused = false;

  private readonly fibers = new Map<number, Fiber>();
  private readyQueue: Fiber[] = [];
  private readonly sleepers: { fiber: Fiber; wakeAt: number }[] = [];
  private readonly mutexes = new Map<string, Mutex>();
  private readonly channels = new Map<string, Rendezvous>();
  private readonly resources = new Map<string, Resource>();
  private readonly inbox = new Map<number, unknown>();
  private readonly listeners: TransitionListener[] = [];
  private beforeInstructionHook: BeforeInstructionHook | null = null;

  // ---- 调试器接口 ------------------------------------------------------

  onTransition(listener: TransitionListener): void {
    this.listeners.push(listener);
  }

  setBeforeInstructionHook(hook: BeforeInstructionHook | null): void {
    this.beforeInstructionHook = hook;
  }

  pauseClock(): void {
    this.clockPaused = true;
  }

  resumeClock(): void {
    this.clockPaused = false;
  }

  // ---- 协程管理 --------------------------------------------------------

  spawn(name: string, body: FiberBody): Fiber {
    const fiber = new Fiber(name, body);
    this.fibers.set(fiber.id, fiber);
    this.readyQueue.push(fiber);
    return fiber;
  }

  getFiber(idOrName: number | string): Fiber | undefined {
    if (typeof idOrName === 'number') return this.fibers.get(idOrName);
    for (const fiber of this.fibers.values()) {
      if (fiber.name === idOrName) return fiber;
    }
    return undefined;
  }

  allFibers(): Fiber[] {
    return [...this.fibers.values()];
  }

  // ---- 时钟 ------------------------------------------------------------

  /** 全局时钟前进一个 tick；时钟被调试器暂停时拒绝执行 */
  advanceTick(): void {
    if (this.clockPaused) {
      throw new Error(`全局时钟已被调试器暂停（tick=${this.tick}），请通过调试器推动`);
    }
    this.tickOnce();
  }

  /** 调试器专用：无论时钟是否暂停都前进一个 tick */
  tickOnce(): void {
    this.tick++;
    // 1. 唤醒到期睡眠者
    for (let i = this.sleepers.length - 1; i >= 0; i--) {
      if (this.sleepers[i].wakeAt <= this.tick) {
        const { fiber } = this.sleepers[i];
        this.sleepers.splice(i, 1);
        this.wake(fiber, `睡眠到期，唤醒 @tick=${this.tick}`);
      }
    }
    // 2. 本 tick 内每个就绪协程执行一条指令
    const runnable = this.readyQueue;
    this.readyQueue = [];
    for (const fiber of runnable) {
      if (fiber.state === 'ready') {
        this.executeOne(fiber);
      }
    }
  }

  /**
   * 单步执行指定协程的一条指令（不改变全局时钟）。
   * 仅当协程处于 ready / paused-breakpoint 时有效；返回是否真正执行。
   */
  stepFiberOnce(fiber: Fiber, force = false): boolean {
    if (fiber.state === 'paused-breakpoint' && !force) return false;
    if (fiber.state !== 'ready' && fiber.state !== 'paused-breakpoint') return false;
    this.readyQueue = this.readyQueue.filter((f) => f !== fiber);
    this.executeOne(fiber, force);
    return true;
  }

  // ---- 指令解释 --------------------------------------------------------

  private executeOne(fiber: Fiber, ignoreHook = false): void {
    if (!ignoreHook && this.beforeInstructionHook && !this.beforeInstructionHook(fiber)) {
      return; // 被调试器拦截（断点）
    }
    const value = this.inbox.get(fiber.id);
    this.inbox.delete(fiber.id);
    const ins = fiber.advance(value);
    if (ins === null) {
      this.emit(fiber, 'running', 'done', '协程执行完毕');
      return;
    }
    this.apply(fiber, ins);
  }

  private apply(fiber: Fiber, ins: Instruction): void {
    const note = describeInstruction(ins);
    switch (ins.op) {
      case 'nop':
        this.makeReady(fiber, note);
        break;
      case 'sleep': {
        const wakeAt = this.tick + ins.ticks;
        this.sleepers.push({ fiber, wakeAt });
        this.suspend(fiber, 'sleeping', { kind: 'sleep', wakeAtTick: wakeAt }, note);
        break;
      }
      case 'lock': {
        const mutex = this.mutexOf(ins.name);
        if (mutex.holder === null) {
          mutex.holder = fiber;
          this.makeReady(fiber, `${note} → 获得锁`);
        } else {
          mutex.queue.push(fiber);
          this.suspend(fiber, 'waiting-lock', { kind: 'lock', name: ins.name },
            `${note} → 锁被 fiber#${mutex.holder.id} 持有`);
        }
        break;
      }
      case 'unlock': {
        const mutex = this.mutexOf(ins.name);
        mutex.holder = null;
        const next = mutex.queue.shift();
        if (next) {
          mutex.holder = next;
          this.wake(next, `锁 "${ins.name}" 移交`);
        }
        this.makeReady(fiber, note);
        break;
      }
      case 'send': {
        const chan = this.channelOf(ins.channel);
        const recver = chan.recvQueue.shift();
        if (recver) {
          this.inbox.set(recver.id, ins.value);
          this.wake(recver, `Channel "${ins.channel}" 收到值`);
          this.makeReady(fiber, `${note} → 直接交付`);
        } else {
          chan.sendQueue.push({ fiber, value: ins.value });
          this.suspend(fiber, 'waiting-channel-send', { kind: 'channel-send', name: ins.channel }, note);
        }
        break;
      }
      case 'recv': {
        const chan = this.channelOf(ins.channel);
        const pending = chan.sendQueue.shift();
        if (pending) {
          this.inbox.set(fiber.id, pending.value);
          this.wake(pending.fiber, `Channel "${ins.channel}" 的值被接收`);
          this.makeReady(fiber, `${note} → 收到值`);
        } else {
          chan.recvQueue.push(fiber);
          this.suspend(fiber, 'waiting-channel-recv', { kind: 'channel-recv', name: ins.channel }, note);
        }
        break;
      }
      case 'require': {
        const res = this.resourceOf(ins.resource);
        if (res.initialized) {
          this.inbox.set(fiber.id, res.value);
          this.makeReady(fiber, `${note} → 资源已就绪`);
        } else {
          res.waiters.push(fiber);
          this.suspend(fiber, 'waiting-resource', { kind: 'resource', name: ins.resource },
            `${note} → 资源未初始化`);
        }
        break;
      }
      case 'provide': {
        const res = this.resourceOf(ins.resource);
        res.initialized = true;
        res.value = ins.value;
        for (const waiter of res.waiters.splice(0)) {
          this.inbox.set(waiter.id, ins.value);
          this.wake(waiter, `资源 "${ins.resource}" 已初始化`);
        }
        this.makeReady(fiber, note);
        break;
      }
      case 'spawn': {
        this.spawn(ins.name, ins.body);
        this.makeReady(fiber, note);
        break;
      }
    }
  }

  // ---- 状态迁移辅助 ------------------------------------------------------

  private makeReady(fiber: Fiber, note: string): void {
    const from = fiber.state;
    fiber.resume();
    this.readyQueue.push(fiber);
    this.emit(fiber, from, 'ready', note);
  }

  private wake(fiber: Fiber, note: string): void {
    this.makeReady(fiber, note);
  }

  private suspend(fiber: Fiber, state: FiberState, reason: NonNullable<Fiber['suspendReason']>, note: string): void {
    const from = fiber.state;
    fiber.suspend(state, reason);
    this.emit(fiber, from, state, note);
  }

  private emit(fiber: Fiber, from: FiberState, to: FiberState, note: string): void {
    if (from === to) return;
    const transition: Transition = {
      tick: this.tick,
      fiberId: fiber.id,
      fiberName: fiber.name,
      from,
      to,
      pc: fiber.pc,
      note,
    };
    for (const listener of this.listeners) listener(transition);
  }

  private mutexOf(name: string): Mutex {
    let mutex = this.mutexes.get(name);
    if (!mutex) {
      mutex = { holder: null, queue: [] };
      this.mutexes.set(name, mutex);
    }
    return mutex;
  }

  private channelOf(name: string): Rendezvous {
    let chan = this.channels.get(name);
    if (!chan) {
      chan = { recvQueue: [], sendQueue: [] };
      this.channels.set(name, chan);
    }
    return chan;
  }

  private resourceOf(name: string): Resource {
    let res = this.resources.get(name);
    if (!res) {
      res = { initialized: false, value: undefined, waiters: [] };
      this.resources.set(name, res);
    }
    return res;
  }
}
