import { Lsdb } from './lsdb.ts';
import { buildGraph, diffGraphs, fullSpf, incrementalSpf } from './spf.ts';
import type { Adjacency, FibEntry, LSA, NodeId } from './types.ts';
import type { EventLog } from './logger.ts';
import type { Network } from './network.ts';

const MAX_HOLD_DOWN = 8;
const FLAP_WINDOW = 10;
const FLAP_THRESHOLD = 4;

export class Router {
  readonly id: NodeId;
  private net: Network;
  private log: EventLog;

  lsdb = new Lsdb();
  interfaces = new Map<NodeId, number>();
  fib = new Map<NodeId, FibEntry>();

  spfDue: number | null = null;
  holdDown = 0;
  spfRuns = 0;
  dirty = false;

  private seq = 0;
  private dist = new Map<NodeId, number>();
  private prev = new Map<NodeId, NodeId | null>();
  private graph: Adjacency | null = null;
  private changeTicks: number[] = [];
  private lastChangeTick: number | null = null;

  constructor(id: NodeId, net: Network, log: EventLog) {
    this.id = id;
    this.net = net;
    this.log = log;
  }

  originate(tick: number): void {
    this.seq++;
    const lsa: LSA = {
      originator: this.id,
      seq: this.seq,
      links: [...this.interfaces].map(([neighbor, cost]) => ({ neighbor, cost })),
    };
    this.lsdb.apply(lsa);
    this.log.add(
      tick,
      this.id,
      `originate LSA #${lsa.seq} links=[${lsa.links.map((l) => `${l.neighbor}/${l.cost}`).join(' ')}]`,
    );
    this.net.flood(this.id, lsa);
    this.onLsdbChange(tick);
  }

  receive(lsa: LSA, from: NodeId, tick: number): void {
    const result = this.lsdb.apply(lsa);
    if (result !== 'applied') return;
    this.log.add(tick, this.id, `recv LSA ${lsa.originator}#${lsa.seq} from ${from}, flooding on`);
    this.net.floodExcept(this.id, lsa, from);
    this.onLsdbChange(tick);
  }

  private onLsdbChange(tick: number): void {
    if (this.lastChangeTick !== tick) {
      this.changeTicks.push(tick);
      this.lastChangeTick = tick;
    }
    this.changeTicks = this.changeTicks.filter((t) => tick - t < FLAP_WINDOW);
    if (this.spfRuns > 0 && this.changeTicks.length >= FLAP_THRESHOLD) {
      const next = Math.min(Math.max(1, this.holdDown * 2), MAX_HOLD_DOWN);
      if (next !== this.holdDown) {
        this.holdDown = next;
        this.log.add(
          tick,
          this.id,
          `route oscillation avoidance: LSDB flapping (${this.changeTicks.length} changed ticks/${FLAP_WINDOW}), SPF hold-down backoff -> ${this.holdDown} ticks`,
        );
      }
    }
    this.spfDue = tick + this.holdDown;
    if (this.holdDown > 0) {
      this.log.add(tick, this.id, `LSDB changed, SPF deferred to tick ${this.spfDue} (hold-down ${this.holdDown})`);
    }
  }

  decay(tick: number): void {
    if (
      this.holdDown > 0 &&
      this.lastChangeTick !== null &&
      tick - this.lastChangeTick >= FLAP_WINDOW
    ) {
      this.holdDown = 0;
      this.changeTicks = [];
      this.log.add(tick, this.id, 'route oscillation avoidance: quiet window reached, SPF hold-down reset to 0');
    }
  }

  runSpf(tick: number): void {
    this.spfDue = null;
    this.spfRuns++;
    const newGraph = buildGraph(this.lsdb);
    let dist: Map<NodeId, number>;
    let prev: Map<NodeId, NodeId | null>;
    let mode: string;
    if (this.graph === null) {
      ({ dist, prev } = fullSpf(newGraph, this.id));
      mode = 'full';
    } else {
      const worsened = diffGraphs(this.graph, newGraph);
      const result = incrementalSpf(newGraph, this.id, this.dist, this.prev, worsened);
      dist = result.dist;
      prev = result.prev;
      mode = `incremental (recomputed ${result.affectedCount}/${newGraph.size} nodes)`;
    }
    this.dist = dist;
    this.prev = prev;
    this.graph = newGraph;

    const newFib = new Map<NodeId, FibEntry>();
    for (const [dest, d] of dist) {
      if (dest === this.id || !isFinite(d)) continue;
      let hop = dest;
      let parent = prev.get(hop);
      while (parent !== null && parent !== undefined && parent !== this.id) {
        hop = parent;
        parent = prev.get(hop);
      }
      newFib.set(dest, { nextHop: hop, cost: d });
    }

    const changes: string[] = [];
    for (const [dest, entry] of newFib) {
      const old = this.fib.get(dest);
      if (!old) changes.push(`+${dest} via ${entry.nextHop} cost ${entry.cost}`);
      else if (old.nextHop !== entry.nextHop || old.cost !== entry.cost) {
        changes.push(`~${dest} via ${entry.nextHop} cost ${entry.cost} (was via ${old.nextHop} cost ${old.cost})`);
      }
    }
    for (const dest of this.fib.keys()) {
      if (!newFib.has(dest)) changes.push(`-${dest} withdrawn`);
    }
    this.fib = newFib;
    this.log.add(
      tick,
      this.id,
      `SPF ${mode} run #${this.spfRuns}: ${changes.length} FIB changes${changes.length ? ': ' + changes.join('; ') : ''}`,
    );
  }
}
