import type { Frame } from './frame.ts';
import type { Endpoint } from './endpoint.ts';

export type FrameObserver = (frame: Frame, from: Endpoint, to: Endpoint) => void;

/**
 * In-memory point-to-point link between two endpoints. Delivers whole frames
 * asynchronously (preserving per-direction order) with a configurable
 * latency, and lets tests/demos attach observers to inspect traffic.
 */
export class Link {
  private readonly latencyMs: number;
  private readonly observers: FrameObserver[] = [];

  constructor(latencyMs = 0) {
    this.latencyMs = latencyMs;
  }

  connect(a: Endpoint, b: Endpoint): void {
    a.attach((frame) => this.deliver(frame, a, b));
    b.attach((frame) => this.deliver(frame, b, a));
    // Handshake: both sides advertise their initial receive window.
    queueMicrotask(() => {
      a.advertise();
      b.advertise();
    });
  }

  addObserver(observer: FrameObserver): void {
    this.observers.push(observer);
  }

  private deliver(frame: Frame, from: Endpoint, to: Endpoint): void {
    for (const observer of this.observers) observer(frame, from, to);
    if (this.latencyMs <= 0) {
      queueMicrotask(() => to.handleFrame(frame));
    } else {
      setTimeout(() => to.handleFrame(frame), this.latencyMs);
    }
  }
}
