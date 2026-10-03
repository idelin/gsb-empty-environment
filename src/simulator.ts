import { Rng, fnv1a } from './rng.ts';
import { EventQueue } from './event-queue.ts';
import { VirtualSwitch, DEFAULT_LINK, type LinkProfile } from './network.ts';
import { SwimNode, type NodeContext, type SwimMessage } from './swim/node.ts';

interface Delivery {
  from: string;
  to: string;
  msg: SwimMessage;
}

/**
 * Single-process discrete-event simulator.
 *
 * One virtual clock, one seeded RNG, one event queue. Each step() advances
 * the clock by one tick: first all frames scheduled for this tick are
 * delivered (in deterministic queue order), then every node runs its SWIM
 * tick in sorted-id order. Given the same seed and scenario, the event log
 * is bit-for-bit reproducible.
 */
export class Simulator {
  now = 0;
  readonly rng: Rng;
  readonly net: VirtualSwitch;
  readonly nodes = new Map<string, SwimNode>();
  readonly logLines: string[] = [];

  private queue = new EventQueue<Delivery>();
  private contexts = new Map<string, NodeContext>();

  constructor(seed: number, defaultLink: LinkProfile = DEFAULT_LINK) {
    this.rng = new Rng(seed);
    this.net = new VirtualSwitch(this.rng, defaultLink);
  }

  addNode(node: SwimNode): void {
    this.nodes.set(node.id, node);
    const sim = this;
    this.contexts.set(node.id, {
      get now() {
        return sim.now;
      },
      rng: this.rng,
      send: (to, msg) => sim.send(node.id, to, msg),
      log: (entry) => sim.log(entry),
    });
  }

  send(from: string, to: string, msg: SwimMessage): void {
    const delay = this.net.route(from, to);
    if (delay === null) {
      this.log(`t=${this.now} drop ${from}->${to} ${msg.kind}`);
      return;
    }
    this.queue.push(this.now + delay, { from, to, msg });
  }

  log(entry: string): void {
    this.logLines.push(entry);
  }

  /** Advance the virtual clock by one discrete tick. */
  step(): void {
    this.now++;
    while (this.queue.peekTime() === this.now) {
      const delivery = this.queue.pop() as Delivery;
      const node = this.nodes.get(delivery.to);
      const ctx = this.contexts.get(delivery.to);
      if (node && ctx) node.onMessage(ctx, delivery.msg);
    }
    for (const id of [...this.nodes.keys()].sort()) {
      const node = this.nodes.get(id) as SwimNode;
      node.tick(this.contexts.get(id) as NodeContext);
    }
  }

  run(steps: number): void {
    for (let i = 0; i < steps; i++) this.step();
  }

  /** Fingerprint of the full event log: identical runs => identical hash. */
  logHash(): string {
    return fnv1a(this.logLines.join('\n'));
  }
}
