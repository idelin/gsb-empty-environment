import type { Effect } from './effects.ts';
import { Fiber, type FiberBody, type FiberState } from './fiber.ts';

/** 一次状态迁移记录：某协程在某个时隙由 from 变为 to。 */
export interface Transition {
  tick: number;
  fiberId: string;
  from: FiberState;
  to: FiberState;
  reason: string;
}

interface LockSlot {
  owner: string | null;
  waiters: Fiber[];
}

interface ChannelSlot {
  queue: unknown[];
  recvWaiters: Fiber[];
}

/**
 * 协作式 Fiber 调度器。
 * 全局时钟以 tick 为单位；每个 tick 唤醒到期睡眠者，
 * 随后每个就绪协程执行一条指令（一次 yield）。
 */
export class Scheduler {
  clock = 0;
  clockPaused = false;

  readonly fibers: Fiber[] = [];
  /** 状态迁移字典：fiberId -> 按时间顺序的迁移记录。 */
  readonly transitions = new Map<string, Transition[]>();

  private readonly locks = new Map<string, LockSlot>();
  private readonly channels = new Map<string, ChannelSlot>();
  private readonly resources = new Map<string, unknown>();

  spawn(body: FiberBody, name?: string): Fiber {
    const fiber = new Fiber(body, name);
    this.fibers.push(fiber);
    this.transitions.set(fiber.id, []);
    return fiber;
  }

  setState(fiber: Fiber, to: FiberState, reason: string, waitingOn: string | null = null): void {
    const from = fiber.state;
    if (from === to && fiber.waitingOn === waitingOn) return;
    fiber.state = to;
    fiber.waitingOn = waitingOn;
    this.transitions.get(fiber.id)!.push({ tick: this.clock, fiberId: fiber.id, from, to, reason });
  }

  /** 推进一个时隙：时钟 +1（若未暂停），唤醒到期睡眠者。 */
  tick(): void {
    if (!this.clockPaused) {
      this.clock += 1;
      for (const fiber of this.fibers) {
        if (fiber.state === 'sleeping' && fiber.wakeAtTick !== null && fiber.wakeAtTick <= this.clock) {
          fiber.wakeAtTick = null;
          this.setState(fiber, 'ready', 'sleep elapsed');
        }
      }
    }
  }

  /**
   * 让指定协程执行一条指令。
   * 仅就绪（ready）协程可执行；返回 false 表示该协程被阻塞或已结束。
   */
  stepFiber(fiber: Fiber): boolean {
    if (fiber.state !== 'ready') return false;
    this.setState(fiber, 'running', `execute instruction #${fiber.pc}`);
    const effect = fiber.next();
    if (effect === null) {
      this.setState(fiber, 'done', 'coroutine returned');
      return true;
    }
    this.applyEffect(fiber, effect);
    return true;
  }

  /** 一个调度轮次：每个就绪协程各执行一条指令。 */
  runReady(): void {
    for (const fiber of this.fibers) {
      if (fiber.state === 'ready') this.stepFiber(fiber);
    }
  }

  /** 自动运行直到所有协程结束或全部阻塞（死锁/假死）。返回执行的指令数。 */
  runToQuiescence(maxTicks = 10_000): number {
    let executed = 0;
    for (let i = 0; i < maxTicks; i += 1) {
      this.tick();
      const before = this.totalPc();
      this.runReady();
      executed += this.totalPc() - before;
      if (this.fibers.every((f) => f.state === 'done')) break;
      if (this.fibers.every((f) => f.state !== 'ready')) break; // 全部挂起：假死
    }
    return executed;
  }

  private totalPc(): number {
    return this.fibers.reduce((sum, f) => sum + f.pc, 0);
  }

  private applyEffect(fiber: Fiber, effect: Effect): void {
    switch (effect.kind) {
      case 'sleep': {
        fiber.wakeAtTick = this.clock + effect.ticks;
        this.setState(fiber, 'sleeping', `sleep ${effect.ticks} tick(s), wake at ${fiber.wakeAtTick}`);
        break;
      }
      case 'lock.acquire': {
        const slot = this.lockSlot(effect.lock);
        if (slot.owner === null) {
          slot.owner = fiber.id;
          this.setState(fiber, 'ready', `acquired lock "${effect.lock}"`);
        } else {
          slot.waiters.push(fiber);
          this.setState(fiber, 'waiting-lock', `lock "${effect.lock}" held by ${slot.owner}`, effect.lock);
        }
        break;
      }
      case 'lock.release': {
        const slot = this.lockSlot(effect.lock);
        slot.owner = null;
        const next = slot.waiters.shift();
        if (next) {
          slot.owner = next.id;
          this.setState(next, 'ready', `granted lock "${effect.lock}"`);
        }
        this.setState(fiber, 'ready', `released lock "${effect.lock}"`);
        break;
      }
      case 'chan.send': {
        const slot = this.channelSlot(effect.channel);
        const waiter = slot.recvWaiters.shift();
        if (waiter) {
          waiter.inbox = effect.value;
          this.setState(waiter, 'ready', `received from channel "${effect.channel}"`);
        } else {
          slot.queue.push(effect.value);
        }
        this.setState(fiber, 'ready', `sent to channel "${effect.channel}"`);
        break;
      }
      case 'chan.recv': {
        const slot = this.channelSlot(effect.channel);
        if (slot.queue.length > 0) {
          fiber.inbox = slot.queue.shift();
          this.setState(fiber, 'ready', `received buffered value from "${effect.channel}"`);
        } else {
          slot.recvWaiters.push(fiber);
          this.setState(fiber, 'waiting-channel', `channel "${effect.channel}" empty`, effect.channel);
        }
        break;
      }
      case 'resource.wait': {
        if (this.resources.has(effect.resource)) {
          fiber.inbox = this.resources.get(effect.resource);
          this.setState(fiber, 'ready', `resource "${effect.resource}" available`);
        } else {
          this.setState(
            fiber,
            'waiting-resource',
            `resource "${effect.resource}" not initialized`,
            effect.resource,
          );
        }
        break;
      }
      case 'resource.provide': {
        this.resources.set(effect.resource, effect.value);
        for (const waiter of this.fibers) {
          if (waiter.state === 'waiting-resource' && waiter.waitingOn === effect.resource) {
            waiter.inbox = effect.value;
            this.setState(waiter, 'ready', `resource "${effect.resource}" provided`);
          }
        }
        this.setState(fiber, 'ready', `provided resource "${effect.resource}"`);
        break;
      }
    }
  }

  private lockSlot(name: string): LockSlot {
    let slot = this.locks.get(name);
    if (!slot) {
      slot = { owner: null, waiters: [] };
      this.locks.set(name, slot);
    }
    return slot;
  }

  private channelSlot(name: string): ChannelSlot {
    let slot = this.channels.get(name);
    if (!slot) {
      slot = { queue: [], recvWaiters: [] };
      this.channels.set(name, slot);
    }
    return slot;
  }
}
