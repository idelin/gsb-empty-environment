/**
 * 因果广播（Causal Broadcast）可靠中间件。
 *
 * 核心机制：
 * - 发送端：每次广播前推进本地向量时钟，并将时钟副本附加到消息上。
 * - 接收端：消息可交付当且仅当
 *     1) 它是发送者序列中的下一条：V[sender] == local[sender] + 1
 *     2) 它的全部因果依赖均已交付：对所有 k != sender，V[k] <= local[k]
 *   不满足条件的消息进入隔离缓存队列（hold-back queue）；
 *   每次交付后自动扫描缓存，级联交付所有变为可交付的消息。
 */

import { type VectorClock, get, tick, merge } from "./vectorClock.ts";

export interface Message {
  /** 全局唯一 ID：`<发送者>#<发送者序号>` */
  id: string;
  sender: string;
  /** 发送时刻的向量时钟快照，即该消息的全部因果依赖 */
  clock: VectorClock;
  payload: unknown;
}

export type DeliverCallback = (message: Message) => void;
export type Transport = (message: Message) => void;

export class CausalBroadcast {
  private readonly nodeId: string;
  private clock: VectorClock = {};
  /** 隔离队列：暂存因因果依赖未满足而无法交付的乱序消息 */
  private holdBackQueue: Message[] = [];
  private readonly deliveredIds = new Set<string>();
  private readonly listeners: DeliverCallback[] = [];
  private transport: Transport = () => {};

  constructor(nodeId: string) {
    this.nodeId = nodeId;
  }

  /** 注册应用层交付回调。 */
  onDeliver(callback: DeliverCallback): void {
    this.listeners.push(callback);
  }

  /** 设置网络发送通道（中间件只负责因果逻辑，不关心具体传输）。 */
  setTransport(transport: Transport): void {
    this.transport = transport;
  }

  /** 广播一条消息：附加本地向量时钟，本地交付后经传输层发出。 */
  broadcast(payload: unknown): Message {
    this.clock = tick(this.clock, this.nodeId);
    const message: Message = {
      id: `${this.nodeId}#${get(this.clock, this.nodeId)}`,
      sender: this.nodeId,
      clock: { ...this.clock },
      payload,
    };
    this.deliverCore(message);
    this.drainQueue();
    this.transport(message);
    return message;
  }

  /** 接收来自网络的消息（可能乱序、可能重复）。 */
  receive(raw: Message): void {
    if (this.deliveredIds.has(raw.id)) return; // 去重
    const message: Message = { ...raw, clock: { ...raw.clock } };
    if (this.isDeliverable(message)) {
      this.deliverCore(message);
      this.drainQueue();
    } else {
      this.holdBackQueue.push(message);
    }
  }

  /** 当前隔离队列中暂存的消息数（用于观测与测试）。 */
  get bufferedCount(): number {
    return this.holdBackQueue.length;
  }

  /** 当前本地向量时钟的只读快照。 */
  get currentClock(): VectorClock {
    return { ...this.clock };
  }

  /**
   * 可交付判定（标准因果广播条件）：
   * 1) 发送者维度恰好是下一条，保证同一发送者的消息按序交付；
   * 2) 其余维度的依赖均已包含在本地时钟中，保证因果依赖先于本消息交付。
   */
  private isDeliverable(message: Message): boolean {
    if (get(message.clock, message.sender) !== get(this.clock, message.sender) + 1) {
      return false;
    }
    for (const key of Object.keys(message.clock)) {
      if (key !== message.sender && get(message.clock, key) > get(this.clock, key)) {
        return false;
      }
    }
    return true;
  }

  private deliverCore(message: Message): void {
    this.clock = merge(this.clock, message.clock);
    this.deliveredIds.add(message.id);
    for (const listener of this.listeners) {
      listener(message);
    }
  }

  /** 级联交付：反复扫描隔离队列，交付所有因新交付而变得可交付的消息。 */
  private drainQueue(): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (let i = 0; i < this.holdBackQueue.length; i++) {
        if (this.isDeliverable(this.holdBackQueue[i])) {
          const [message] = this.holdBackQueue.splice(i, 1);
          this.deliverCore(message);
          progressed = true;
          break;
        }
      }
    }
  }
}
