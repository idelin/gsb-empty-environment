import { EventEmitter } from 'node:events';
import { EMPTY_PAYLOAD, FrameType, type Frame } from './frame.ts';

export interface SenderOptions {
  /** Maximum payload bytes per DATA frame. Default 16 KiB. */
  maxPayloadSize?: number;
  /** Interval between zero-window probes. Default 50 ms. */
  probeIntervalMs?: number;
}

export interface FlowEvent {
  bytesInFlight: number;
  window: number;
  queuedBytes: number;
}

/**
 * Sliding-window sender.
 *
 * The sender strictly bounds bytes in flight by the most recent window beacon
 * received from the peer: a DATA frame may only be transmitted while
 * `bytesInFlight < peerWindow`. When the in-flight bytes fill the window the
 * sender suspends; when the window shrinks to zero it additionally enters
 * probe mode, emitting WINDOW_PROBE frames until the peer re-opens the window
 * with a beacon.
 *
 * Events:
 *  - 'suspend' (FlowEvent)  in-flight bytes reached the advertised window
 *  - 'resume'  (FlowEvent)  a beacon reopened usable window space
 *  - 'probe'    ()           a zero-window probe was sent
 *  - 'drain'   ()           queue empty and everything acknowledged
 */
export class SlidingWindowSender extends EventEmitter {
  private readonly sendFrame: (frame: Frame) => void;
  private readonly maxPayloadSize: number;
  private readonly probeIntervalMs: number;

  private queue: Buffer[] = [];
  private queuedBytes = 0;
  /** Oldest unacknowledged byte offset (left edge of the window). */
  private sendBase = 0;
  /** Next byte offset to transmit. */
  private nextSeq = 0;
  /** Latest window beacon from the peer. Starts closed until the handshake. */
  private peerWindow = 0;

  private suspended = false;
  private drained = true;
  private probeTimer: NodeJS.Timeout | null = null;

  readonly stats = {
    dataFramesSent: 0,
    dataBytesSent: 0,
    probesSent: 0,
    suspensions: 0,
    beaconsReceived: 0,
  };

  constructor(sendFrame: (frame: Frame) => void, options: SenderOptions = {}) {
    super();
    this.sendFrame = sendFrame;
    this.maxPayloadSize = options.maxPayloadSize ?? 16 * 1024;
    this.probeIntervalMs = options.probeIntervalMs ?? 50;
  }

  get bytesInFlight(): number {
    return this.nextSeq - this.sendBase;
  }

  get currentWindow(): number {
    return this.peerWindow;
  }

  get pendingBytes(): number {
    return this.queuedBytes + this.bytesInFlight;
  }

  get isSuspended(): boolean {
    return this.suspended;
  }

  get nextSeqOffset(): number {
    return this.nextSeq;
  }

  /** Queue application bytes for transmission. */
  send(data: Buffer): void {
    if (data.length === 0) return;
    this.queue.push(Buffer.from(data));
    this.queuedBytes += data.length;
    this.drained = false;
    this.pump();
  }

  /**
   * Consume a flow-control beacon (cumulative ACK + advertised window).
   * Called for every incoming frame, since all frames piggyback beacon values.
   */
  handleBeacon(ack: number, window: number): void {
    this.stats.beaconsReceived++;
    if (ack > this.sendBase) {
      this.sendBase = Math.min(ack, this.nextSeq);
    }
    this.peerWindow = window;
    if (window > 0) this.stopProbe();
    this.pump();
    if (window === 0 && this.pendingBytes > 0) this.startProbe();
  }

  /** Stop probing and timers. Safe to call multiple times. */
  close(): void {
    this.stopProbe();
  }

  /** Resolves once the queue is empty and all bytes are acknowledged. */
  waitForDrain(): Promise<void> {
    if (this.queuedBytes === 0 && this.bytesInFlight === 0) return Promise.resolve();
    return new Promise((resolve) => this.once('drain', resolve));
  }

  private pump(): void {
    while (this.queuedBytes > 0) {
      const allowance = this.peerWindow - this.bytesInFlight;
      if (allowance <= 0) break;
      const head = this.queue[0];
      const size = Math.min(head.length, allowance, this.maxPayloadSize);
      const chunk = Buffer.from(head.subarray(0, size));
      if (size === head.length) {
        this.queue.shift();
      } else {
        this.queue[0] = head.subarray(size);
      }
      this.queuedBytes -= size;
      const frame: Frame = {
        type: FrameType.DATA,
        seq: this.nextSeq,
        ack: 0, // stamped by the endpoint with its own beacon values
        window: 0,
        payload: chunk,
      };
      this.nextSeq += size;
      this.stats.dataFramesSent++;
      this.stats.dataBytesSent += size;
      this.sendFrame(frame);
    }
    this.updateFlowState();
  }

  private updateFlowState(): void {
    const blocked =
      this.queuedBytes > 0 && this.bytesInFlight >= this.peerWindow;
    if (blocked && !this.suspended) {
      this.suspended = true;
      this.stats.suspensions++;
      this.emit('suspend', this.flowEvent());
      if (this.peerWindow === 0) this.startProbe();
    } else if (!blocked && this.suspended) {
      this.suspended = false;
      this.emit('resume', this.flowEvent());
    }
    if (!this.drained && this.queuedBytes === 0 && this.bytesInFlight === 0) {
      this.drained = true;
      this.emit('drain');
    }
  }

  private flowEvent(): FlowEvent {
    return {
      bytesInFlight: this.bytesInFlight,
      window: this.peerWindow,
      queuedBytes: this.queuedBytes,
    };
  }

  private startProbe(): void {
    if (this.probeTimer !== null) return;
    this.probeTimer = setInterval(() => {
      if (this.peerWindow !== 0 || this.pendingBytes === 0) {
        this.stopProbe();
        return;
      }
      this.stats.probesSent++;
      this.emit('probe');
      this.sendFrame({
        type: FrameType.WINDOW_PROBE,
        seq: this.nextSeq,
        ack: 0,
        window: 0,
        payload: EMPTY_PAYLOAD,
      });
    }, this.probeIntervalMs);
    this.probeTimer.unref();
  }

  private stopProbe(): void {
    if (this.probeTimer !== null) {
      clearInterval(this.probeTimer);
      this.probeTimer = null;
    }
  }
}
