import type { DeterministicRng } from './rng.ts';
import type { PacketPayload } from './events.ts';

/**
 * Simulated network stack. All state lives in plain data structures and is
 * fully captured by snapshot()/restore() — no hidden references, no I/O.
 */
export class NetworkStack {
  private rxQueue: PacketPayload[] = [];
  private delivered = 0;
  private dropped = 0;
  private bytesReceived = 0;
  private congestionWindow = 4;

  static readonly RX_CAPACITY = 8;

  onPacket(packet: PacketPayload, rng: DeterministicRng): void {
    this.bytesReceived += packet.size;
    // Deterministic "loss": ~1/8 of bursts drop a packet, driven by the
    // kernel RNG so replays reproduce the exact same drops.
    const lossy = rng.nextInt(8) === 0;
    if (lossy || this.rxQueue.length >= NetworkStack.RX_CAPACITY) {
      this.dropped += 1;
      return;
    }
    this.rxQueue.push(packet);
    // Drain as many packets as the congestion window allows.
    const budget = Math.min(this.congestionWindow, this.rxQueue.length);
    for (let i = 0; i < budget; i += 1) {
      this.rxQueue.shift();
      this.delivered += 1;
    }
    // Deterministic window control: grow on clean delivery, halve on loss.
    this.congestionWindow = Math.min(this.congestionWindow + 1, 16);
  }

  onTimeout(rng: DeterministicRng): void {
    // Timeout => congestion event: shrink window, flush retransmit queue.
    this.congestionWindow = Math.max(1, this.congestionWindow >> 1);
    const retransmits = Math.min(this.rxQueue.length, rng.nextInt(3));
    for (let i = 0; i < retransmits; i += 1) {
      this.rxQueue.shift();
      this.delivered += 1;
    }
  }

  snapshot(): unknown {
    return {
      rxQueue: this.rxQueue.map((p) => ({ ...p })),
      delivered: this.delivered,
      dropped: this.dropped,
      bytesReceived: this.bytesReceived,
      congestionWindow: this.congestionWindow,
    };
  }
}

/**
 * Simulated timer/clock-interrupt service. Pending timers and the fired
 * history are part of the reproducible memory invariants.
 */
export class TimerService {
  private pending: Array<{ timerId: string; armedAt: number }> = [];
  private fired: string[] = [];

  arm(timerId: string, atTick: number): void {
    this.pending.push({ timerId, armedAt: atTick });
  }

  fire(timerId: string): void {
    this.pending = this.pending.filter((t) => t.timerId !== timerId);
    this.fired.push(timerId);
  }

  snapshot(): unknown {
    return {
      pending: this.pending.map((t) => ({ ...t })),
      fired: [...this.fired],
    };
  }
}
