import { EventEmitter } from "node:events";
import { FLAG_PROBE, type Frame } from "./frame.ts";

interface UnackedSegment {
  seq: number;
  payload: Buffer;
}

export interface SenderOptions {
  /** 最大报文段大小（字节），默认 1460 */
  mss?: number;
  /** 零窗口探测间隔（毫秒），默认 100 */
  probeIntervalMs?: number;
  /** 收到首个信标前的对端窗口初值，默认 0（严格等待信标授权） */
  initialPeerWindow?: number;
}

export interface FlowState {
  bytesInFlight: number;
  peerWindow: number;
}

/**
 * 滑动窗口发送协调器。
 *
 * 不变式：bytesInFlight <= peerWindow（最新信标通告的接收窗口）。
 * 窗口被在途数据填满（或归零）时立即挂起发送；窗口归零进入探测模式，
 * 周期性发送 1 字节 PROBE 帧，直到收到窗口扩大的 ACK 才恢复发送。
 *
 * 事件：
 *  - "frame"   (frame: Frame)      待发送的数据/探测帧
 *  - "suspend" (state: FlowState)  在途数据填满窗口，发送挂起
 *  - "resume"  (state: FlowState)  收到窗口扩大 ACK，恢复发送
 *  - "probe"   (info: {seq})       发出一次零窗口探测
 *  - "idle"                        发送队列与未确认段均已清空
 */
export class SlidingWindowSender extends EventEmitter {
  readonly mss: number;
  readonly probeIntervalMs: number;
  /** 对端最新通告的接收窗口（由 ACK 信标更新） */
  peerWindow: number;
  /** 在途字节数：已发送但尚未被累积确认的字节 */
  bytesInFlight = 0;
  /** 下一个待分配的字节序号 */
  nextSeq = 0;

  private queue: Buffer[] = [];
  private queuedBytes = 0;
  private unacked: UnackedSegment[] = [];
  private probeTimer: NodeJS.Timeout | null = null;
  private suspended = false;
  private idleEmitted = true;

  constructor(options: SenderOptions = {}) {
    super();
    this.mss = options.mss ?? 1460;
    this.probeIntervalMs = options.probeIntervalMs ?? 100;
    this.peerWindow = options.initialPeerWindow ?? 0;
  }

  /** 尚未被确认的字节总量（在途 + 排队） */
  get pendingBytes(): number {
    return this.queuedBytes + this.bytesInFlight;
  }

  get isSuspended(): boolean {
    return this.suspended;
  }

  /** 应用层注入数据 */
  enqueue(data: Buffer): void {
    if (data.length === 0) return;
    this.queue.push(data);
    this.queuedBytes += data.length;
    this.idleEmitted = false;
    this.tryFlush();
  }

  /** 处理收到的流量控制信标（ACK 帧头中的 ack + window） */
  handleBeacon(ack: number, window: number): void {
    this.trimUnacked(ack);
    this.peerWindow = window;
    if (window > 0) {
      this.stopProbe();
      this.tryFlush();
    } else if (this.pendingBytes > 0) {
      this.startProbe();
      this.updateSuspension();
    }
    this.maybeIdle();
  }

  close(): void {
    this.stopProbe();
  }

  private availableWindow(): number {
    return this.peerWindow - this.bytesInFlight;
  }

  private tryFlush(): void {
    let sent = false;
    while (this.queuedBytes > 0 && this.availableWindow() > 0) {
      const grant = Math.min(this.mss, this.availableWindow());
      const payload = this.takeFromQueue(grant);
      const seq = this.nextSeq;
      this.nextSeq += payload.length;
      this.unacked.push({ seq, payload });
      this.bytesInFlight += payload.length;
      const frame: Frame = { flags: 0, seq, ack: 0, window: 0, payload };
      this.emit("frame", frame);
      sent = true;
    }
    // 挂起状态下因窗口扩大而重新发出数据 -> 视为恢复发送
    if (sent && this.suspended) {
      this.suspended = false;
      const state: FlowState = { bytesInFlight: this.bytesInFlight, peerWindow: this.peerWindow };
      this.emit("resume", state);
    }
    this.updateSuspension();
    this.maybeIdle();
  }

  private takeFromQueue(limit: number): Buffer {
    const head = this.queue[0];
    if (head.length <= limit) {
      this.queue.shift();
      this.queuedBytes -= head.length;
      return head;
    }
    const part = head.subarray(0, limit);
    this.queue[0] = head.subarray(limit);
    this.queuedBytes -= limit;
    return part;
  }

  /** 累积确认：丢弃 ack 之前的未确认段 */
  private trimUnacked(ack: number): void {
    while (this.unacked.length > 0) {
      const first = this.unacked[0];
      const end = first.seq + first.payload.length;
      if (ack >= end) {
        this.bytesInFlight -= first.payload.length;
        this.unacked.shift();
      } else if (ack > first.seq) {
        const cut = ack - first.seq;
        first.payload = first.payload.subarray(cut);
        first.seq = ack;
        this.bytesInFlight -= cut;
      } else {
        break;
      }
    }
  }

  private updateSuspension(): void {
    const shouldSuspend = this.queuedBytes > 0 && this.availableWindow() <= 0;
    if (shouldSuspend && !this.suspended) {
      this.suspended = true;
      const state: FlowState = { bytesInFlight: this.bytesInFlight, peerWindow: this.peerWindow };
      this.emit("suspend", state);
    } else if (!shouldSuspend && this.suspended) {
      this.suspended = false;
      const state: FlowState = { bytesInFlight: this.bytesInFlight, peerWindow: this.peerWindow };
      this.emit("resume", state);
    }
  }

  private startProbe(): void {
    if (this.probeTimer !== null) return;
    this.probeTimer = setInterval(() => this.sendProbe(), this.probeIntervalMs);
    this.probeTimer.unref();
  }

  private stopProbe(): void {
    if (this.probeTimer !== null) {
      clearInterval(this.probeTimer);
      this.probeTimer = null;
    }
  }

  /** 零窗口探测：发送 1 字节 PROBE 帧，不计入在途、不消耗序号 */
  private sendProbe(): void {
    if (this.peerWindow !== 0 || this.pendingBytes === 0) {
      this.stopProbe();
      return;
    }
    const source = this.unacked.length > 0 ? this.unacked[0] : null;
    const seq = source ? source.seq : this.nextSeq;
    const payload = source
      ? source.payload.subarray(0, 1)
      : this.queue[0].subarray(0, 1);
    this.emit("probe", { seq });
    const frame: Frame = { flags: FLAG_PROBE, seq, ack: 0, window: 0, payload: Buffer.from(payload) };
    this.emit("frame", frame);
  }

  private maybeIdle(): void {
    if (this.queuedBytes === 0 && this.unacked.length === 0 && !this.idleEmitted) {
      this.idleEmitted = true;
      this.stopProbe();
      this.emit("idle");
    }
  }
}
