import { EventEmitter } from "node:events";

export interface Beacon {
  /** 累积确认号：下一个期望接收的字节序号 */
  ack: number;
  /** 当前接收窗口（字节）= 容量 - 已缓冲 */
  window: number;
}

/**
 * 窗口化接收协调器。
 *
 * 接收窗口 = capacity - buffered。每当窗口变化（收数据、应用消费、
 * 容量调整）都会发出 "beacon" 事件，由端点捎带/单独封装进 ACK 帧头，
 * 作为发送端唯一的发送授权依据。
 *
 * 事件：
 *  - "beacon" (beacon: Beacon)   窗口状态变化，需向对端通告
 *  - "data"   (buffered: number) 有新的按序数据到达，可消费
 */
export class WindowedReceiver extends EventEmitter {
  private capacity: number;
  private chunks: Buffer[] = [];
  /** 已缓冲、尚未被应用消费的字节数 */
  buffered = 0;
  /** 下一个期望接收的字节序号 */
  expectedSeq = 0;

  constructor(capacity: number) {
    super();
    if (capacity <= 0) throw new RangeError("capacity must be positive");
    this.capacity = capacity;
  }

  get window(): number {
    return Math.max(0, this.capacity - this.buffered);
  }

  get beacon(): Beacon {
    return { ack: this.expectedSeq, window: this.window };
  }

  /**
   * 处理收到的数据帧。返回 true 表示按序接收。
   * 乱序、重复、超窗的段一律丢弃并回告当前信标；
   * PROBE 帧只用于引出信标，payload 不交付。
   */
  onData(seq: number, payload: Buffer, isProbe: boolean): boolean {
    if (isProbe) {
      this.emit("beacon", this.beacon);
      return false;
    }
    if (seq !== this.expectedSeq || payload.length > this.window) {
      this.emit("beacon", this.beacon);
      return false;
    }
    this.chunks.push(payload);
    this.buffered += payload.length;
    this.expectedSeq += payload.length;
    this.emit("beacon", this.beacon);
    this.emit("data", this.buffered);
    return true;
  }

  /** 应用层消费（窗口随之扩大，并触发信标通告） */
  consume(limit: number): Buffer {
    const parts: Buffer[] = [];
    let remaining = Math.min(limit, this.buffered);
    let took = 0;
    while (remaining > 0 && this.chunks.length > 0) {
      const head = this.chunks[0];
      if (head.length <= remaining) {
        parts.push(head);
        this.chunks.shift();
        remaining -= head.length;
        took += head.length;
      } else {
        parts.push(head.subarray(0, remaining));
        this.chunks[0] = head.subarray(remaining);
        took += remaining;
        remaining = 0;
      }
    }
    if (took > 0) {
      this.buffered -= took;
      this.emit("beacon", this.beacon);
    }
    return Buffer.concat(parts);
  }

  /** 调整接收容量（窗口缩小/扩大通知由此产生） */
  resize(newCapacity: number): void {
    if (newCapacity <= 0) throw new RangeError("capacity must be positive");
    this.capacity = newCapacity;
    this.emit("beacon", this.beacon);
  }
}
