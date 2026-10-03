import { EventEmitter } from "node:events";
import { decodeFrame, encodeFrame, FLAG_ACK, FLAG_PROBE, type Frame } from "./frame.ts";
import { SlidingWindowSender, type SenderOptions } from "./sender.ts";
import { WindowedReceiver } from "./receiver.ts";
import type { Transport } from "./transport.ts";

/**
 * 双向流控端点：每个端点同时持有发送协调器与接收协调器。
 * 所有出站帧（数据帧 / 纯 ACK 帧）的帧头都捎带本端最新信标
 * （ack + window），实现双向流量控制信标机制。
 *
 * 事件（转发自内部协调器）："data" | "suspend" | "resume" | "probe" | "idle"
 */
export class FlowEndpoint extends EventEmitter {
  readonly sender: SlidingWindowSender;
  readonly receiver: WindowedReceiver;
  private transport: Transport | null = null;

  constructor(recvCapacity: number, senderOptions: SenderOptions = {}) {
    super();
    this.sender = new SlidingWindowSender(senderOptions);
    this.receiver = new WindowedReceiver(recvCapacity);

    this.sender.on("frame", (frame: Frame) => this.transmit(frame));
    this.receiver.on("beacon", () => this.sendBeacon());
    this.receiver.on("data", (buffered: number) => this.emit("data", buffered));
    for (const event of ["suspend", "resume", "probe", "idle"] as const) {
      this.sender.on(event, (info: unknown) => this.emit(event, info));
    }
  }

  /** 绑定传输层，并立即向对端通告初始接收窗口 */
  attach(transport: Transport): void {
    this.transport = transport;
    transport.onMessage((raw) => this.onRawFrame(raw));
    this.sendBeacon();
  }

  /** 应用层发送数据（受滑动窗口约束） */
  send(data: Buffer): void {
    this.sender.enqueue(data);
  }

  /** 应用层消费接收缓冲（触发窗口扩大 ACK） */
  consume(limit: number): Buffer {
    return this.receiver.consume(limit);
  }

  /** 调整接收容量（下发窗口缩小/扩大通知） */
  resizeWindow(newCapacity: number): void {
    this.receiver.resize(newCapacity);
  }

  close(): void {
    this.sender.close();
  }

  private transmit(frame: Frame): void {
    if (!this.transport) return;
    const beacon = this.receiver.beacon;
    this.transport.send(
      encodeFrame({
        ...frame,
        flags: frame.flags | FLAG_ACK,
        ack: beacon.ack,
        window: beacon.window,
      }),
    );
  }

  private sendBeacon(): void {
    if (!this.transport) return;
    const beacon = this.receiver.beacon;
    const frame: Frame = {
      flags: FLAG_ACK,
      seq: this.sender.nextSeq,
      ack: beacon.ack,
      window: beacon.window,
      payload: Buffer.alloc(0),
    };
    this.transport.send(encodeFrame(frame));
  }

  private onRawFrame(raw: Buffer): void {
    const frame = decodeFrame(raw);
    if ((frame.flags & FLAG_ACK) !== 0) {
      this.sender.handleBeacon(frame.ack, frame.window);
    }
    if (frame.payload.length > 0 || (frame.flags & FLAG_PROBE) !== 0) {
      this.receiver.onData(frame.seq, frame.payload, (frame.flags & FLAG_PROBE) !== 0);
    }
  }
}
