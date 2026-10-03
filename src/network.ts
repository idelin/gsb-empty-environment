import { EventLog } from './logger.ts';
import { Router } from './node.ts';
import type { LSA, NodeId } from './types.ts';

interface Link {
  a: NodeId;
  b: NodeId;
  cost: number;
  up: boolean;
}

export interface TraceResult {
  ok: boolean;
  path: NodeId[];
  reason?: string;
}

export class Network {
  nodes = new Map<NodeId, Router>();
  links = new Map<string, Link>();
  tick = 0;
  log = new EventLog();

  private queue: Array<{ to: NodeId; from: NodeId; lsa: LSA }> = [];

  static key(a: NodeId, b: NodeId): string {
    return [a, b].sort().join('-');
  }

  addNode(id: NodeId): Router {
    const router = new Router(id, this, this.log);
    this.nodes.set(id, router);
    router.dirty = true;
    return router;
  }

  addLink(a: NodeId, b: NodeId, cost: number): void {
    this.links.set(Network.key(a, b), { a, b, cost, up: true });
    (this.nodes.get(a) as Router).interfaces.set(b, cost);
    (this.nodes.get(b) as Router).interfaces.set(a, cost);
  }

  cutLink(a: NodeId, b: NodeId): void {
    const link = this.links.get(Network.key(a, b));
    if (!link || !link.up) return;
    link.up = false;
    (this.nodes.get(a) as Router).interfaces.delete(b);
    (this.nodes.get(b) as Router).interfaces.delete(a);
    (this.nodes.get(a) as Router).dirty = true;
    (this.nodes.get(b) as Router).dirty = true;
    this.log.add(this.tick, 'NET', `*** link ${a}-${b} CUT (fiber failure) ***`);
  }

  restoreLink(a: NodeId, b: NodeId): void {
    const link = this.links.get(Network.key(a, b));
    if (!link || link.up) return;
    link.up = true;
    (this.nodes.get(a) as Router).interfaces.set(b, link.cost);
    (this.nodes.get(b) as Router).interfaces.set(a, link.cost);
    (this.nodes.get(a) as Router).dirty = true;
    (this.nodes.get(b) as Router).dirty = true;
    this.log.add(this.tick, 'NET', `*** link ${a}-${b} restored ***`);
  }

  setLinkCost(a: NodeId, b: NodeId, cost: number): void {
    const link = this.links.get(Network.key(a, b));
    if (!link) return;
    link.cost = cost;
    if (link.up) {
      (this.nodes.get(a) as Router).interfaces.set(b, cost);
      (this.nodes.get(b) as Router).interfaces.set(a, cost);
      (this.nodes.get(a) as Router).dirty = true;
      (this.nodes.get(b) as Router).dirty = true;
    }
    this.log.add(this.tick, 'NET', `*** link ${a}-${b} cost -> ${cost} ***`);
  }

  flood(from: NodeId, lsa: LSA): void {
    for (const neighbor of (this.nodes.get(from) as Router).interfaces.keys()) {
      this.queue.push({ to: neighbor, from, lsa });
    }
  }

  floodExcept(from: NodeId, lsa: LSA, except: NodeId): void {
    for (const neighbor of (this.nodes.get(from) as Router).interfaces.keys()) {
      if (neighbor !== except) this.queue.push({ to: neighbor, from, lsa });
    }
  }

  /** Advance one discrete tick: fire SPF timers, originate LSAs, flood, run due SPFs. */
  step(): void {
    this.tick++;
    for (const router of this.nodes.values()) {
      if (router.spfDue !== null && router.spfDue <= this.tick) router.runSpf(this.tick);
    }
    for (const router of this.nodes.values()) router.decay(this.tick);
    for (const router of this.nodes.values()) {
      if (router.dirty) {
        router.dirty = false;
        router.originate(this.tick);
      }
    }
    while (this.queue.length > 0) {
      const msg = this.queue.shift() as { to: NodeId; from: NodeId; lsa: LSA };
      (this.nodes.get(msg.to) as Router).receive(msg.lsa, msg.from, this.tick);
    }
    for (const router of this.nodes.values()) {
      if (router.spfDue !== null && router.spfDue <= this.tick) router.runSpf(this.tick);
    }
  }

  isConverged(): boolean {
    if (this.queue.length > 0) return false;
    for (const router of this.nodes.values()) {
      if (router.dirty || router.spfDue !== null) return false;
    }
    return true;
  }

  runUntilConverged(maxTicks = 50): number {
    for (let i = 0; i < maxTicks; i++) {
      if (this.isConverged()) return this.tick;
      this.step();
    }
    return this.isConverged() ? this.tick : -1;
  }

  /** Simulate forwarding a packet src -> dst through the current FIBs. */
  traceRoute(src: NodeId, dst: NodeId): TraceResult {
    const path: NodeId[] = [src];
    const visited = new Set<NodeId>([src]);
    let current = src;
    while (current !== dst) {
      const router = this.nodes.get(current) as Router;
      const entry = router.fib.get(dst);
      if (!entry) {
        return { ok: false, path, reason: `blackhole at ${current}: no FIB entry for ${dst}` };
      }
      const link = this.links.get(Network.key(current, entry.nextHop));
      if (!link || !link.up) {
        return { ok: false, path, reason: `blackhole at ${current}: next-hop link ${current}-${entry.nextHop} is down` };
      }
      if (visited.has(entry.nextHop)) {
        return { ok: false, path: [...path, entry.nextHop], reason: `loop detected: ${[...path, entry.nextHop].join(' -> ')}` };
      }
      visited.add(entry.nextHop);
      path.push(entry.nextHop);
      current = entry.nextHop;
    }
    return { ok: true, path };
  }

  checkAllPairs(): string[] {
    const violations: string[] = [];
    for (const src of this.nodes.keys()) {
      for (const dst of this.nodes.keys()) {
        if (src === dst) continue;
        const result = this.traceRoute(src, dst);
        if (!result.ok) violations.push(`${src} -> ${dst}: ${result.reason}`);
      }
    }
    return violations;
  }
}
