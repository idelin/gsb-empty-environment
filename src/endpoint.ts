import { EMPTY_PAYLOAD, FrameType, type Frame } from './frame.ts';
import { FlowControlledReceiver } from './receiver.ts';
import { SlidingWindowSender } from './sender.ts';

export interface EndpointOptions {
  name?: string;
  /** Receive-buffer capacity of this endpoint. */
  capacity: number;
  maxPayloadSize?: number;
  probeIntervalMs?: number;
}

/**
 * Bidirectional endpoint: owns one sliding-window sender and one
 * flow-controlled receiver. Every outgoing frame is stamped with the
 * receiver's beacon values (cumulative ACK + window), so flow-control
 * signalling rides on both pure ACK frames and DATA frames alike.
 */
export class Endpoint {
  readonly name: string;
  readonly sender: SlidingWindowSender;
  readonly receiver: FlowControlledReceiver;

  private out: (frame: Frame) => void = () => {};

  constructor(options: EndpointOptions) {
    this.name = options.name ?? 'endpoint';
    this.sender = new SlidingWindowSender((frame) => this.transmit(frame), {
      maxPayloadSize: options.maxPayloadSize,
      probeIntervalMs: options.probeIntervalMs,
    });
    this.receiver = new FlowControlledReceiver({
      capacity: options.capacity,
      onBeacon: (ack, window) =>
        this.transmit({
          type: FrameType.ACK,
          seq: this.sender.nextSeqOffset,
          ack,
          window,
          payload: EMPTY_PAYLOAD,
        }),
    });
  }

  /** Wire the endpoint to its transport (used by Link). */
  attach(out: (frame: Frame) => void): void {
    this.out = out;
  }

  /** Advertise the current window (handshake / unsolicited beacon). */
  advertise(): void {
    this.receiver.sendBeacon();
  }

  /** Queue application bytes for transmission to the peer. */
  send(data: Buffer): void {
    this.sender.send(data);
  }

  /** Entry point for frames arriving from the peer. */
  handleFrame(frame: Frame): void {
    // Every frame is a beacon: update the sender's view of the peer window.
    this.sender.handleBeacon(frame.ack, frame.window);
    if (frame.type === FrameType.DATA) {
      this.receiver.handleData(frame.seq, frame.payload);
    } else if (frame.type === FrameType.WINDOW_PROBE) {
      this.receiver.handleProbe();
    }
  }

  close(): void {
    this.sender.close();
    this.receiver.stopAutoConsume();
  }

  private transmit(frame: Frame): void {
    // Piggyback this endpoint's own beacon on every outgoing frame.
    frame.ack = this.receiver.nextExpected;
    frame.window = this.receiver.window;
    this.out(frame);
  }
}
