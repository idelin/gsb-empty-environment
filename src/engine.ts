import { ScrollNode, buildScene, hitChain, pathToRoot } from './tree.ts';
import type { SceneFixture } from './tree.ts';
import type { Axis, Vec2 } from './types.ts';
import { cloneVec } from './types.ts';

export type TransactionKind = 'wheel' | 'inertia' | 'programmatic';

/** Per-layer, per-axis accounting. Invariant: received + residualBefore === consumed + passed + residualAfter. */
export interface AxisLedger {
  received: number;
  consumed: number;
  passed: number;
  residualBefore: number;
  residualAfter: number;
  offsetBefore: number;
  offsetAfter: number;
}

export interface LayerRecord {
  nodeId: string;
  /** Set when the node's axis transform rewrote the incoming delta (applied exactly once). */
  transformApplied: 'swap' | null;
  x: AxisLedger;
  y: AxisLedger;
}

export type TransactionInput =
  | { kind: 'wheel'; point: Vec2; delta: Vec2 }
  | { kind: 'inertia'; point: Vec2; velocity: Vec2; decay: number; maxSteps: number }
  | { kind: 'programmatic'; nodeId: string; target: Vec2; source: string };

export interface ScrollTransaction {
  id: number;
  kind: TransactionKind;
  /** Links momentum transactions back to the gesture that spawned them. */
  originId: number | null;
  source: string;
  input: TransactionInput;
  layers: LayerRecord[];
  /** Delta that reached the root and had nowhere to go. */
  discarded: Vec2;
}

export type LogEntry =
  | { type: 'transaction'; transaction: ScrollTransaction }
  | { type: 'capture'; nodeId: string | null };

export interface ScrollEvent {
  nodeId: string;
  offset: Vec2;
  delta: Vec2;
  source: string;
  transactionId: number;
  kind: TransactionKind;
}

export type ScrollListener = (event: ScrollEvent) => void;

interface ConsumeResult {
  consumed: number;
  remaining: number;
  residualBefore: number;
  residualAfter: number;
  offsetBefore: number;
  offsetAfter: number;
}

function snap(value: number, step: number): number {
  if (step <= 0) return value;
  return Math.sign(value) * Math.round(Math.abs(value) / step) * step;
}

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value));
}

/**
 * Consume as much of `amount` as the node can absorb on `axis`.
 *
 * The node's residual (sub-snap remainder from earlier events) is folded in
 * first, so fractional deltas accumulate across events and small reverse
 * deltas cancel stored remainders before moving the offset back. Whatever
 * exceeds the scroll range is returned to the caller and propagates outward;
 * only the snapping remainder stays behind as residual.
 */
export function consumeAt(node: ScrollNode, axis: Axis, amount: number): ConsumeResult {
  const residualBefore = node.residual[axis];
  const offsetBefore = node.offset[axis];
  const noop: ConsumeResult = {
    consumed: 0,
    remaining: amount,
    residualBefore,
    residualAfter: residualBefore,
    offsetBefore,
    offsetAfter: offsetBefore,
  };
  if (!node.allows(axis) || amount === 0 && residualBefore === 0) return noop;

  const incoming = amount + residualBefore;
  const dir = Math.sign(incoming);
  const max = node.maxScroll(axis);
  const room = dir > 0 ? max - offsetBefore : dir < 0 ? offsetBefore : 0;
  const applied = Math.abs(incoming) <= room ? incoming : dir * room;

  let newOffset = offsetBefore + applied;
  if (node.snapStep > 0) newOffset = snap(newOffset, node.snapStep);
  newOffset = clamp(newOffset, 0, max);

  const actual = newOffset - offsetBefore;
  node.offset[axis] = newOffset;
  node.residual[axis] = applied - actual || 0; // normalize -0 so ledgers compare cleanly

  return {
    consumed: actual,
    remaining: incoming - applied,
    residualBefore,
    residualAfter: node.residual[axis],
    offsetBefore,
    offsetAfter: newOffset,
  };
}

const MAX_EMISSION_DEPTH = 8;

export class ScrollEngine {
  readonly root: ScrollNode;
  readonly nodes: Map<string, ScrollNode>;
  readonly log: LogEntry[] = [];
  /** Programmatic scroll attempts rejected because they echoed their own source token. */
  blockedFeedback = 0;
  /** Emissions rejected because the reentrant callback chain got too deep. */
  blockedDepth = 0;

  private nextTransactionId = 1;
  private captureNodeId: string | null = null;
  private listeners: ScrollListener[] = [];
  private emissionToken: string | null = null;
  private emissionDepth = 0;

  constructor(fixture: SceneFixture) {
    const scene = buildScene(fixture);
    this.root = scene.root;
    this.nodes = scene.nodes;
  }

  onScroll(listener: ScrollListener): void {
    this.listeners.push(listener);
  }

  setCapture(nodeId: string | null): void {
    this.captureNodeId = nodeId;
    this.log.push({ type: 'capture', nodeId });
  }

  private chainFor(point: Vec2): ScrollNode[] {
    if (this.captureNodeId !== null) {
      const captured = this.nodes.get(this.captureNodeId);
      if (captured) return pathToRoot(captured);
    }
    return hitChain(this.root, point);
  }

  dispatchWheel(
    point: Vec2,
    delta: Vec2,
    opts: { kind?: TransactionKind; originId?: number | null; source?: string } = {},
  ): ScrollTransaction {
    const kind = opts.kind ?? 'wheel';
    const source = opts.source ?? 'user';
    const cursor = cloneVec(delta);
    const layers: LayerRecord[] = [];

    for (const node of this.chainFor(point)) {
      let transformApplied: 'swap' | null = null;
      if (node.transform === 'swap') {
        const tmp = cursor.x;
        cursor.x = cursor.y;
        cursor.y = tmp;
        transformApplied = 'swap';
      }
      const received = cloneVec(cursor);
      const rx = consumeAt(node, 'x', cursor.x);
      const ry = consumeAt(node, 'y', cursor.y);
      cursor.x = rx.remaining;
      cursor.y = ry.remaining;
      layers.push({
        nodeId: node.id,
        transformApplied,
        x: {
          received: received.x,
          consumed: rx.consumed,
          passed: rx.remaining,
          residualBefore: rx.residualBefore,
          residualAfter: rx.residualAfter,
          offsetBefore: rx.offsetBefore,
          offsetAfter: rx.offsetAfter,
        },
        y: {
          received: received.y,
          consumed: ry.consumed,
          passed: ry.remaining,
          residualBefore: ry.residualBefore,
          residualAfter: ry.residualAfter,
          offsetBefore: ry.offsetBefore,
          offsetAfter: ry.offsetAfter,
        },
      });
      if (cursor.x === 0 && cursor.y === 0) break;
    }

    const transaction: ScrollTransaction = {
      id: this.nextTransactionId++,
      kind,
      originId: opts.originId ?? null,
      source,
      input: { kind: 'wheel', point: cloneVec(point), delta: cloneVec(delta) },
      layers,
      discarded: cursor,
    };
    this.log.push({ type: 'transaction', transaction });
    this.emitChanges(layers, transaction);
    return transaction;
  }

  /**
   * Momentum tail: a deterministic, decaying series of deltas replayed through
   * the exact same transaction pipeline. Every step is its own transaction
   * linked by originId, so no delta can be consumed twice and residuals carry
   * across steps through the nodes themselves.
   */
  startInertia(
    point: Vec2,
    velocity: Vec2,
    opts: { decay?: number; maxSteps?: number; source?: string } = {},
  ): ScrollTransaction[] {
    const decay = opts.decay ?? 0.5;
    const maxSteps = opts.maxSteps ?? 64;
    const source = opts.source ?? 'inertia';
    const originId = this.nextTransactionId;
    const transactions: ScrollTransaction[] = [];
    const v = cloneVec(velocity);
    for (let step = 0; step < maxSteps; step += 1) {
      if (Math.abs(v.x) < 0.05 && Math.abs(v.y) < 0.05) break;
      transactions.push(this.dispatchWheel(point, v, { kind: 'inertia', originId, source }));
      v.x *= decay;
      v.y *= decay;
    }
    const first = transactions[0];
    if (first) {
      first.input = {
        kind: 'inertia',
        point: cloneVec(point),
        velocity: cloneVec(velocity),
        decay,
        maxSteps,
      };
    }
    return transactions;
  }

  /**
   * Programmatic scroll. Emits scroll events tagged with the caller's source
   * token; a listener that echoes a scroll back with the same token is
   * recognised as feedback and dropped.
   */
  scrollTo(nodeId: string, target: Vec2, opts: { source?: string } = {}): ScrollTransaction | null {
    const source = opts.source ?? 'programmatic';
    if (source === this.emissionToken) {
      this.blockedFeedback += 1;
      return null;
    }
    const node = this.nodes.get(nodeId);
    if (!node) throw new Error(`unknown node ${nodeId}`);

    const layers: LayerRecord[] = [
      {
        nodeId,
        transformApplied: null,
        x: this.programmaticAxis(node, 'x', target.x),
        y: this.programmaticAxis(node, 'y', target.y),
      },
    ];
    const transaction: ScrollTransaction = {
      id: this.nextTransactionId++,
      kind: 'programmatic',
      originId: null,
      source,
      input: { kind: 'programmatic', nodeId, target: cloneVec(target), source },
      layers,
      discarded: { x: layers[0]!.x.passed, y: layers[0]!.y.passed },
    };
    this.log.push({ type: 'transaction', transaction });
    this.emitChanges(layers, transaction);
    return transaction;
  }

  private programmaticAxis(node: ScrollNode, axis: Axis, targetValue: number): AxisLedger {
    const offsetBefore = node.offset[axis];
    const residualBefore = node.residual[axis];
    const max = node.maxScroll(axis);
    let newOffset = node.snapStep > 0 ? snap(targetValue, node.snapStep) : targetValue;
    newOffset = clamp(newOffset, 0, max);
    node.offset[axis] = newOffset;
    node.residual[axis] = 0;
    const requested = targetValue - offsetBefore;
    const consumed = newOffset - offsetBefore;
    return {
      received: requested,
      consumed,
      // Absolute positioning also discards any stored residual; account for it
      // so received + residualBefore === consumed + passed + residualAfter holds.
      passed: requested + residualBefore - consumed,
      residualBefore,
      residualAfter: 0,
      offsetBefore,
      offsetAfter: newOffset,
    };
  }

  private emitChanges(layers: LayerRecord[], transaction: ScrollTransaction): void {
    if (this.emissionDepth >= MAX_EMISSION_DEPTH) {
      this.blockedDepth += 1;
      return;
    }
    this.emissionDepth += 1;
    const previousToken = this.emissionToken;
    this.emissionToken = transaction.source;
    try {
      for (const layer of layers) {
        const dx = layer.x.consumed;
        const dy = layer.y.consumed;
        if (dx === 0 && dy === 0) continue;
        const node = this.nodes.get(layer.nodeId)!;
        const event: ScrollEvent = {
          nodeId: layer.nodeId,
          offset: cloneVec(node.offset),
          delta: { x: dx, y: dy },
          source: transaction.source,
          transactionId: transaction.id,
          kind: transaction.kind,
        };
        for (const listener of this.listeners) listener(event);
      }
    } finally {
      this.emissionToken = previousToken;
      this.emissionDepth -= 1;
    }
  }

  /** Re-executes a recorded log against a fresh engine and returns it. */
  static replay(fixture: SceneFixture, log: LogEntry[]): ScrollEngine {
    const engine = new ScrollEngine(fixture);
    const inertiaOrigins = new Set<number>();
    for (const entry of log) {
      if (entry.type === 'capture') {
        engine.setCapture(entry.nodeId);
        continue;
      }
      const transaction = entry.transaction;
      // Momentum steps after the first are logged as individual transactions;
      // re-running the recorded startInertia call regenerates them, so skip them.
      if (transaction.originId !== null && inertiaOrigins.has(transaction.originId)) continue;
      const input = transaction.input;
      if (input.kind === 'wheel') {
        engine.dispatchWheel(input.point, input.delta, {
          kind: transaction.kind === 'inertia' ? 'inertia' : 'wheel',
          originId: transaction.originId,
          source: transaction.source,
        });
      } else if (input.kind === 'inertia') {
        inertiaOrigins.add(transaction.id);
        engine.startInertia(input.point, input.velocity, {
          decay: input.decay,
          maxSteps: input.maxSteps,
          source: transaction.source,
        });
      } else {
        engine.scrollTo(input.nodeId, input.target, { source: input.source });
      }
    }
    return engine;
  }
}
