import { Network } from './network.ts';
import { Simulator } from './simulator.ts';
import { ALIVE, DEAD } from './types.ts';
import type { DigestEntry, MemberRecord, Message } from './types.ts';

export interface SubClusterOptions {
  cellSize: number; // nodes per Cluster Cell
  ringDegree: number; // heartbeat successors per node inside a cell
  intraIntervalMs: number; // steady-state intra-cell heartbeat interval
  crossIntervalMs: number; // low-frequency cross-cell aggregation interval
  crossFanout: number; // peer cells contacted per cross-cell round
  suspectTimeoutMs: number; // backup heartbeat-timeout detector (partitions)
  maxDigestEntries: number; // bound on piggybacked change entries
}

export const DEFAULT_OPTIONS: SubClusterOptions = {
  cellSize: 25,
  ringDegree: 2,
  intraIntervalMs: 2000,
  crossIntervalMs: 2000,
  crossFanout: 3,
  suspectTimeoutMs: 6000,
  maxDigestEntries: 32,
};

/**
 * Sub-cluster gossip node.
 *
 * Topology: the flat network is partitioned into virtual Cluster Cells.
 *  - Intra-cell: each node heartbeats to its `ringDegree` ring successors at
 *    `intraIntervalMs` (lazy steady state). Membership *changes* are pushed
 *    immediately (event-driven), so detection latency is decoupled from the
 *    heartbeat interval.
 *  - Crash fast-path: the transport delivers a link reset to ring neighbors
 *    of a crashed node; the first detector confirms DEAD and pushes the event
 *    to every cell member at once.
 *  - Cross-cell: the cell leader (lowest live id) exchanges aggregated cell
 *    digests with a few peer-cell leaders at `crossIntervalMs`. One packet
 *    carries the state of a whole cell, and leaders' digests diffuse back
 *    into cells piggybacked on ordinary heartbeats.
 */
export class SubClusterNode {
  readonly id: number;
  readonly cellId: number;
  crashed = false;
  onDeadDetected: ((deadId: number, atMs: number) => void) | null = null;

  private records = new Map<number, MemberRecord>();
  private lastSeen = new Map<number, number>();
  private recentChanges: DigestEntry[] = [];
  private net: Network;
  private sim: Simulator;
  private rng: () => number;
  private nodeCount: number;
  private opts: SubClusterOptions;
  private cellStart: number;
  private cellCount: number;
  private indexInCell: number;

  constructor(
    id: number,
    nodeCount: number,
    net: Network,
    sim: Simulator,
    rng: () => number,
    opts: SubClusterOptions,
  ) {
    this.id = id;
    this.nodeCount = nodeCount;
    this.net = net;
    this.sim = sim;
    this.rng = rng;
    this.opts = opts;
    this.cellId = Math.floor(id / opts.cellSize);
    this.cellStart = this.cellId * opts.cellSize;
    this.cellCount = Math.min(opts.cellSize, nodeCount - this.cellStart);
    this.indexInCell = id - this.cellStart;
    net.register(id, (msg) => this.handleMessage(msg as Message));
  }

  stateOf(id: number): number {
    return this.recordOf(id).state;
  }

  start(): void {
    this.every(this.opts.intraIntervalMs, this.rng() * this.opts.intraIntervalMs, () =>
      this.heartbeat(),
    );
    this.every(1000, this.rng() * 1000, () => this.checkTimeouts());
    this.every(this.opts.crossIntervalMs, this.rng() * this.opts.crossIntervalMs, () =>
      this.crossCellRound(),
    );
  }

  crash(): void {
    this.crashed = true;
    this.net.unregister(this.id);
  }

  handleMessage(msg: Message): void {
    if (this.crashed) return;
    switch (msg.kind) {
      case 'hb':
        this.lastSeen.set(msg.from, this.sim.now);
        this.merge(msg.entries);
        break;
      case 'event':
        this.merge(msg.entries);
        break;
      case 'digest': {
        this.merge(msg.entries);
        this.net.send(this.id, msg.from, {
          kind: 'digestAck',
          from: this.id,
          cellId: this.cellId,
          entries: this.myCellEntries(),
        });
        break;
      }
      case 'digestAck':
        this.merge(msg.entries);
        break;
      case 'linkReset':
        this.confirmDead(msg.deadId);
        break;
    }
  }

  private every(intervalMs: number, initialDelayMs: number, fn: () => void): void {
    const tick = (): void => {
      if (this.crashed) return;
      fn();
      this.sim.schedule(intervalMs, tick);
    };
    this.sim.schedule(initialDelayMs, tick);
  }

  private successor(k: number): number {
    return this.cellStart + ((this.indexInCell + k) % this.cellCount);
  }

  ringNeighbors(): number[] {
    const out: number[] = [];
    for (let k = 1; k <= this.opts.ringDegree; k++) {
      out.push(this.successor(k));
      out.push(
        this.cellStart +
          ((this.indexInCell - k + this.cellCount) % this.cellCount),
      );
    }
    return out;
  }

  private recordOf(id: number): MemberRecord {
    const existing = this.records.get(id);
    if (existing) return existing;
    return { incarnation: 1, state: ALIVE, version: 0 };
  }

  private heartbeat(): void {
    const entries = this.collectDigest();
    for (let k = 1; k <= this.opts.ringDegree; k++) {
      this.net.send(this.id, this.successor(k), {
        kind: 'hb',
        from: this.id,
        entries,
      });
    }
  }

  private collectDigest(): DigestEntry[] {
    const entries: DigestEntry[] = [{ id: this.id, ...this.recordOf(this.id) }];
    const start = Math.max(0, this.recentChanges.length - this.opts.maxDigestEntries);
    for (let i = start; i < this.recentChanges.length; i++) {
      const change = this.recentChanges[i];
      if (change.id !== this.id) entries.push(change);
    }
    return entries;
  }

  private merge(entries: DigestEntry[]): void {
    for (const entry of entries) {
      if (entry.id === this.id) continue;
      const current = this.recordOf(entry.id);
      const newer =
        entry.version > current.version ||
        (entry.version === current.version && entry.state > current.state);
      if (!newer) continue;
      this.records.set(entry.id, {
        incarnation: entry.incarnation,
        state: entry.state,
        version: entry.version,
      });
      this.rememberChange(entry);
      if (entry.state === DEAD && current.state !== DEAD) this.notifyDead(entry.id);
    }
  }

  private rememberChange(entry: DigestEntry): void {
    this.recentChanges.push(entry);
    if (this.recentChanges.length > this.opts.maxDigestEntries * 2) {
      this.recentChanges.splice(0, this.opts.maxDigestEntries);
    }
  }

  private notifyDead(id: number): void {
    if (this.onDeadDetected) this.onDeadDetected(id, this.sim.now);
  }

  /** Confirm a peer dead locally and push the event to the whole cell. */
  private confirmDead(id: number): void {
    const current = this.recordOf(id);
    if (current.state === DEAD) return;
    const record: MemberRecord = {
      incarnation: current.incarnation,
      state: DEAD,
      version: Math.max(current.version + 1, this.sim.now),
    };
    this.records.set(id, record);
    const entry: DigestEntry = { id, ...record };
    this.rememberChange(entry);
    this.notifyDead(id);
    for (let m = 0; m < this.cellCount; m++) {
      const peer = this.cellStart + m;
      if (peer === this.id || peer === id) continue;
      if (this.stateOf(peer) === DEAD) continue;
      this.net.send(this.id, peer, { kind: 'event', from: this.id, entries: [entry] });
    }
  }

  /** Backup detector for partitions: missing heartbeats from ring successors. */
  private checkTimeouts(): void {
    for (let k = 1; k <= this.opts.ringDegree; k++) {
      const succ = this.successor(k);
      const seen = this.lastSeen.get(succ);
      if (seen !== undefined && this.sim.now - seen > this.opts.suspectTimeoutMs) {
        this.confirmDead(succ);
      }
    }
  }

  private amLeader(): boolean {
    for (let m = 0; m < this.cellCount; m++) {
      const peer = this.cellStart + m;
      if (this.stateOf(peer) === ALIVE) return peer === this.id;
    }
    return false;
  }

  private leaderOfCell(cellId: number): number {
    const start = cellId * this.opts.cellSize;
    const count = Math.min(this.opts.cellSize, this.nodeCount - start);
    for (let m = 0; m < count; m++) {
      const peer = start + m;
      if (this.stateOf(peer) === ALIVE) return peer;
    }
    return start;
  }

  /** Low-frequency aggregated broadcast between cell leaders. */
  private crossCellRound(): void {
    if (!this.amLeader()) return;
    const cells = Math.ceil(this.nodeCount / this.opts.cellSize);
    if (cells < 2) return;
    const chosen = new Set<number>();
    while (chosen.size < Math.min(this.opts.crossFanout, cells - 1)) {
      const cell = Math.floor(this.rng() * cells);
      if (cell !== this.cellId) chosen.add(cell);
    }
    for (const cell of chosen) {
      this.net.send(this.id, this.leaderOfCell(cell), {
        kind: 'digest',
        from: this.id,
        cellId: this.cellId,
        entries: this.myCellEntries(),
      });
    }
  }

  private myCellEntries(): DigestEntry[] {
    const entries: DigestEntry[] = [];
    for (let m = 0; m < this.cellCount; m++) {
      const peer = this.cellStart + m;
      entries.push({ id: peer, ...this.recordOf(peer) });
    }
    return entries;
  }
}
