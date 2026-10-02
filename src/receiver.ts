import { EventEmitter } from 'node:events';

export interface ReceiverOptions {
  /** Receive-buffer capacity; also the maximum advertised window. */
  capacity: number;
  /** Called whenever a beacon (cumulative ACK + window) must be emitted. */
  onBeacon: (ack: number, window: number) => void;
}

export interface AutoConsumeOptions {
  /** Bytes consumed per tick, simulating application read speed. */
  chunkBytes: number;
  intervalMs: number;
}

/**
 * Flow-controlled receiver.
 *
 * Incoming payload bytes sit in a bounded buffer until the application
 * consumes them. The advertised window is always `capacity - buffered`, so a
 * slow consumer automatically shrinks the beacon and throttles the peer.
 * `setCapacity()` actively pushes a window-shrink/grow notification.
 *
 * Events:
 *  - 'data'     (chunk: Buffer)  in-order bytes handed to the application
 *  - 'beacon'   (ack, window)    a beacon was emitted
 */
export class FlowControlledReceiver extends EventEmitter {
  private capacity: number;
  private readonly onBeacon: (ack: number, window: number) => void;

  private chunks: Buffer[] = [];
  private buffered = 0;
  /** Next expected byte offset == cumulative ACK. */
  private expected = 0;
  /**
   * Highest window right edge ever advertised (max of ack + window).
   * Frames already in flight against an earlier, larger beacon must still be
   * accepted after the window shrinks (RFC 793: the receiver must be prepared
   * to accept data up to the window it last advertised).
   */
  private advertisedEdge: number;
  private autoTimer: NodeJS.Timeout | null = null;

  readonly stats = {
    dataFramesReceived: 0,
    dataBytesReceived: 0,
    beaconsSent: 0,
    outOfWindowDrops: 0,
    probesAnswered: 0,
  };

  constructor(options: ReceiverOptions) {
    super();
    if (options.capacity <= 0) throw new RangeError('capacity must be > 0');
    this.capacity = options.capacity;
    this.onBeacon = options.onBeacon;
    // The receiver is always prepared to accept up to a full buffer.
    this.advertisedEdge = options.capacity;
  }

  /** Current advertised window in bytes (never negative). */
  get window(): number {
    return Math.max(0, this.capacity - this.buffered);
  }

  get bufferedBytes(): number {
    return this.buffered;
  }

  get nextExpected(): number {
    return this.expected;
  }

  get maxWindow(): number {
    return this.capacity;
  }

  /** Handle an incoming DATA frame. */
  handleData(seq: number, payload: Buffer): void {
    if (seq !== this.expected) {
      // Lightweight protocol: the link is ordered, so anything unexpected is
      // dropped and re-beaconed (a real implementation would buffer or NACK).
      this.sendBeacon();
      return;
    }
    if (seq + payload.length > this.advertisedEdge) {
      // Defensive: peer violated the advertised window. Drop and re-beacon.
      this.stats.outOfWindowDrops++;
      this.sendBeacon();
      return;
    }
    this.chunks.push(Buffer.from(payload));
    this.buffered += payload.length;
    this.expected += payload.length;
    this.stats.dataFramesReceived++;
    this.stats.dataBytesReceived += payload.length;
    this.sendBeacon();
  }

  /** Answer a zero-window probe with an immediate beacon. */
  handleProbe(): void {
    this.stats.probesAnswered++;
    this.sendBeacon();
  }

  /** Application pull: consume up to `maxBytes` from the receive buffer. */
  consume(maxBytes: number): Buffer {
    let remaining = maxBytes;
    const parts: Buffer[] = [];
    while (remaining > 0 && this.chunks.length > 0) {
      const head = this.chunks[0];
      const size = Math.min(remaining, head.length);
      parts.push(head.subarray(0, size));
      if (size === head.length) {
        this.chunks.shift();
      } else {
        this.chunks[0] = head.subarray(size);
      }
      remaining -= size;
      this.buffered -= size;
    }
    const data = Buffer.concat(parts);
    if (data.length > 0) {
      this.emit('data', data);
      this.sendBeacon(); // window grew: notify the peer
    }
    return data;
  }

  /**
   * Resize the receive-buffer capacity and immediately push a beacon with the
   * resulting window (window-shrink / window-grow notification).
   */
  setCapacity(capacity: number): void {
    if (capacity < 0) throw new RangeError('capacity must be >= 0');
    this.capacity = capacity;
    this.sendBeacon();
  }

  /** Simulate an application consuming at a fixed rate. */
  startAutoConsume(options: AutoConsumeOptions): void {
    this.stopAutoConsume();
    this.autoTimer = setInterval(() => {
      if (this.buffered > 0) {
        this.consume(Math.min(options.chunkBytes, this.buffered));
      }
    }, options.intervalMs);
  }

  stopAutoConsume(): void {
    if (this.autoTimer !== null) {
      clearInterval(this.autoTimer);
      this.autoTimer = null;
    }
  }

  /** Emit a beacon with the cumulative ACK and current window. */
  sendBeacon(): void {
    this.stats.beaconsSent++;
    this.advertisedEdge = Math.max(this.advertisedEdge, this.expected + this.window);
    this.emit('beacon', this.expected, this.window);
    this.onBeacon(this.expected, this.window);
  }
}
