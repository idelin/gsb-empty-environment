// 确定性虚拟时间网络：支持时延、丢包、扣留（hold）/释放（release），
// 用来精确编排“旧包迟到”“确认丢失”“同时重启”等场景。

import type { Message } from './messages.ts';
import { fmtMsg } from './messages.ts';

export interface EndpointLike {
  readonly id: string;
  receive(msg: Message): void;
}

interface PendingPacket {
  id: number;
  from: string;
  to: EndpointLike;
  msg: Message;
  deliverAt: number;
}

interface Timer {
  id: number;
  fireAt: number;
  fn: () => void;
  cancelled: boolean;
}

export class Network {
  now = 0;
  readonly log: string[] = [];

  // 持久拦截钩子：返回 true 的报文一发出即被扣留/丢弃，直到显式释放。
  shouldHold: ((m: Message) => boolean) | null = null;
  shouldDrop: ((m: Message) => boolean) | null = null;

  private nextId = 1;
  private pending: PendingPacket[] = [];
  private held: PendingPacket[] = [];
  private timers: Timer[] = [];

  transmit(from: string, to: EndpointLike, msg: Message, delay = 1): number {
    const pkt: PendingPacket = { id: this.nextId++, from, to, msg, deliverAt: this.now + delay };
    if (this.shouldDrop?.(msg)) {
      this.log.push(`t=${this.now} DROP #${pkt.id} ${from} -> ${to.id} ${fmtMsg(msg)}`);
      return pkt.id;
    }
    if (this.shouldHold?.(msg)) {
      this.held.push(pkt);
      this.log.push(`t=${this.now} HOLD #${pkt.id} ${from} -> ${to.id} ${fmtMsg(msg)}`);
      return pkt.id;
    }
    this.pending.push(pkt);
    this.log.push(`t=${this.now} TX   #${pkt.id} ${from} -> ${to.id} ${fmtMsg(msg)}`);
    return pkt.id;
  }

  setTimer(delay: number, fn: () => void): number {
    const t: Timer = { id: this.nextId++, fireAt: this.now + delay, fn, cancelled: false };
    this.timers.push(t);
    return t.id;
  }

  clearTimer(id: number): void {
    const t = this.timers.find((x) => x.id === id);
    if (t) t.cancelled = true;
  }

  dropWhere(pred: (m: Message) => boolean, limit = Infinity): number {
    let n = 0;
    this.pending = this.pending.filter((p) => {
      if (n < limit && pred(p.msg)) {
        n++;
        this.log.push(`t=${this.now} DROP #${p.id} ${fmtMsg(p.msg)}`);
        return false;
      }
      return true;
    });
    return n;
  }

  holdWhere(pred: (m: Message) => boolean, limit = Infinity): number {
    let n = 0;
    const rest: PendingPacket[] = [];
    for (const p of this.pending) {
      if (n < limit && pred(p.msg)) {
        n++;
        this.held.push(p);
        this.log.push(`t=${this.now} HOLD #${p.id} ${fmtMsg(p.msg)}`);
      } else {
        rest.push(p);
      }
    }
    this.pending = rest;
    return n;
  }

  releaseWhere(pred: (m: Message) => boolean, delay = 1, limit = Infinity): number {
    let n = 0;
    const rest: PendingPacket[] = [];
    for (const p of this.held) {
      if (n < limit && pred(p.msg)) {
        n++;
        p.deliverAt = this.now + delay;
        this.pending.push(p);
        this.log.push(`t=${this.now} RELEASE #${p.id} ${fmtMsg(p.msg)}`);
      } else {
        rest.push(p);
      }
    }
    this.held = rest;
    return n;
  }

  heldCount(pred?: (m: Message) => boolean): number {
    return this.held.filter((p) => !pred || pred(p.msg)).length;
  }

  step(): boolean {
    let nextT = Infinity;
    for (const p of this.pending) nextT = Math.min(nextT, p.deliverAt);
    for (const t of this.timers) if (!t.cancelled) nextT = Math.min(nextT, t.fireAt);
    if (nextT === Infinity) return false;
    this.now = Math.max(this.now, nextT);

    const dueTimers = this.timers
      .filter((t) => !t.cancelled && t.fireAt <= this.now)
      .sort((a, b) => a.id - b.id);
    this.timers = this.timers.filter((t) => !t.cancelled && t.fireAt > this.now);
    for (const t of dueTimers) t.fn();

    const duePkts = this.pending
      .filter((p) => p.deliverAt <= this.now)
      .sort((a, b) => a.id - b.id);
    this.pending = this.pending.filter((p) => p.deliverAt > this.now);
    for (const p of duePkts) {
      this.log.push(`t=${this.now} RX   #${p.id} ${p.to.id} <= ${fmtMsg(p.msg)}`);
      p.to.receive(p.msg);
    }
    return true;
  }

  run(maxSteps = 10000): void {
    let i = 0;
    while (i++ < maxSteps && this.step()) {
      // 推进到网络空闲（无待投递报文、无待触发定时器）
    }
  }
}
