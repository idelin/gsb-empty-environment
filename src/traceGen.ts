// Finite trace generator: enumerates pointer-event traces that probe behavior
// near the gesture thresholds, including second-pointer joins, capture loss,
// delayed events, coordinate-space changes and stale-pointer timeouts.
// Everything is driven by logical sequence numbers / logical time -- no real
// device timing involved.

import { DEFAULT_THRESHOLDS, type PointerEventLike, type Thresholds } from './types.ts';

export type TraceOp =
  | { kind: 'event'; event: PointerEventLike }
  | { kind: 'tick'; time: number };

export interface NamedTrace {
  name: string;
  ops: TraceOp[];
}

const EPS = 0.5;

class TraceBuilder {
  ops: TraceOp[] = [];
  private seq = 0;
  private time = 0;

  private space: string;

  constructor(space: string) {
    this.space = space;
  }

  advance(dt: number): this {
    this.time += dt;
    return this;
  }

  ev(type: PointerEventLike['type'], pointerId: number, x: number, y: number, space?: string): this {
    this.ops.push({
      kind: 'event',
      event: { seq: this.seq++, time: this.time, pointerId, type, x, y, space: space ?? this.space },
    });
    return this;
  }

  down(id: number, x: number, y: number, space?: string): this { return this.ev('pointerdown', id, x, y, space); }
  move(id: number, x: number, y: number, space?: string): this { return this.ev('pointermove', id, x, y, space); }
  up(id: number, x: number, y: number, space?: string): this { return this.ev('pointerup', id, x, y, space); }
  cancel(id: number, x: number, y: number, space?: string): this { return this.ev('pointercancel', id, x, y, space); }
  lostCapture(id: number, x: number, y: number, space?: string): this { return this.ev('lostpointercapture', id, x, y, space); }
  tick(now: number): this { this.ops.push({ kind: 'tick', time: now }); return this; }

  build(name: string): NamedTrace {
    return { name, ops: this.ops };
  }
}

/**
 * Enumerate the finite trace suite for the given thresholds.
 * Move distances are chosen as threshold - eps, threshold, threshold + eps
 * so boundary behavior on both sides is exercised.
 */
export function generateTraces(thresholds: Thresholds = DEFAULT_THRESHOLDS): NamedTrace[] {
  const T = thresholds.drag; // == clickSlop in defaults
  const near = [T - EPS, T, T + EPS];
  const traces: NamedTrace[] = [];

  // 1. Single-pointer click vs drag boundary, with up/cancel/lost-capture endings.
  for (const d of near) {
    traces.push(new TraceBuilder('root')
      .down(1, 50, 50).advance(16).move(1, 50 + d, 50).advance(16).up(1, 50 + d, 50)
      .build(`single-move-${d}-up`));
    traces.push(new TraceBuilder('root')
      .down(1, 50, 50).advance(16).move(1, 50 + d, 50).advance(16).cancel(1, 50 + d, 50)
      .build(`single-move-${d}-cancel`));
    traces.push(new TraceBuilder('root')
      .down(1, 50, 50).advance(16).move(1, 50 + d, 50).advance(16).lostCapture(1, 50 + d, 50)
      .build(`single-move-${d}-lostcapture`));
  }

  // 2. Drag then second pointer joins -> drag must be compensated, pinch takes over.
  for (const d1 of near) {
    for (const spread of [0, 30]) {
      traces.push(new TraceBuilder('root')
        .down(1, 50, 50).advance(16).move(1, 50 + d1, 50).advance(16)
        .down(2, 150, 50).advance(16)
        .move(2, 150 + spread, 50).advance(16)
        .up(2, 150 + spread, 50).advance(16)
        .up(1, 50 + d1, 50)
        .build(`drag-${d1}-then-join-spread-${spread}`));
    }
  }

  // 3. Second pointer joins BEFORE any drag; pinch scale near its threshold.
  const scaleBase = 100;
  for (const ratio of [1 + thresholds.pinchScale - 0.01, 1 + thresholds.pinchScale, 1 + thresholds.pinchScale + 0.01]) {
    const d2 = scaleBase * ratio;
    traces.push(new TraceBuilder('root')
      .down(1, 100, 100).advance(16).down(2, 100 + scaleBase, 100).advance(16)
      .move(2, 100 + d2, 100).advance(16)
      .up(2, 100 + d2, 100).advance(16).up(1, 100, 100)
      .build(`pinch-scale-${ratio.toFixed(3)}`));
  }

  // 4. Capture loss mid-pinch: remaining pointer must not stay active forever.
  traces.push(new TraceBuilder('root')
    .down(1, 100, 100).advance(16).down(2, 200, 100).advance(16)
    .move(2, 260, 100).advance(16)
    .lostCapture(2, 260, 100).advance(16)
    .move(1, 120, 100).advance(16).up(1, 120, 100)
    .build('pinch-then-capture-loss'));
  traces.push(new TraceBuilder('root')
    .down(1, 100, 100).advance(16).down(2, 200, 100).advance(16)
    .lostCapture(1, 100, 100).lostCapture(2, 200, 100)
    .build('both-capture-loss'));

  // 5. Stale pointer expired by tick watchdog (no up ever arrives).
  traces.push(new TraceBuilder('root')
    .down(1, 60, 60).advance(16).move(1, 60 + T + 1, 60)
    .tick(thresholds.staleTimeout + 1000)
    .build('stale-pointer-expired'));
  traces.push(new TraceBuilder('root')
    .down(1, 60, 60).advance(16)
    .tick(thresholds.staleTimeout - 100) // not yet stale
    .advance(16).up(1, 60, 60)
    .build('not-stale-yet'));

  // 6. Delayed events for an already-removed pointer must be dropped.
  traces.push(new TraceBuilder('root')
    .down(1, 40, 40).advance(16).up(1, 40, 40).advance(16)
    .move(1, 400, 400).up(1, 400, 400) // delayed, pointer already gone
    .build('delayed-events-after-up'));
  traces.push(new TraceBuilder('root')
    .down(1, 40, 40).advance(16).down(2, 90, 40).advance(16)
    .up(2, 90, 40).advance(16)
    .move(2, 500, 500) // delayed move for removed pointer
    .up(1, 40, 40)
    .build('delayed-move-between-generations'));

  // 7. Coordinate-space changes interleaved mid-gesture.
  //    'viewport' is root translated by (+10,+20); convertible.
  traces.push(new TraceBuilder('root')
    .down(1, 50, 50).advance(16)
    .move(1, 40, 30, 'viewport').advance(16)   // == (50,50) in root: no drift
    .move(1, 40 + T + 1, 30, 'viewport').advance(16)
    .up(1, 40 + T + 1, 30, 'viewport')
    .build('space-change-convertible'));
  //    'unregistered' space cannot be converted: hard re-anchor, no commit.
  traces.push(new TraceBuilder('root')
    .down(1, 50, 50).advance(16).move(1, 50 + T + 2, 50).advance(16)
    .move(1, 0, 0, 'unregistered').advance(16)
    .move(1, 500, 500, 'unregistered').advance(16)
    .up(1, 500, 500, 'unregistered')
    .build('space-change-unknown'));

  // 8. Click must not fire after a drag preview, even if pointer returns.
  traces.push(new TraceBuilder('root')
    .down(1, 50, 50).advance(16).move(1, 50 + T + 2, 50).advance(16)
    .move(1, 50, 50).advance(16).up(1, 50, 50)
    .build('drag-out-and-back'));

  // 9. Pinch commits once; remaining pointer cannot start a second commit.
  traces.push(new TraceBuilder('root')
    .down(1, 100, 100).advance(16).down(2, 200, 100).advance(16)
    .move(2, 300, 100).advance(16)     // pinch preview
    .up(2, 300, 100).advance(16)       // pinch commits
    .move(1, 100 + T + 5, 100).advance(16) // would-be drag after commit
    .up(1, 100 + T + 5, 100)
    .build('pinch-commit-then-no-drag'));

  // 10. Cancel during drag preview -> compensate, no commit, no click.
  traces.push(new TraceBuilder('root')
    .down(1, 50, 50).advance(16).move(1, 50 + T + 2, 50).advance(16)
    .cancel(1, 50 + T + 2, 50)
    .build('cancel-during-drag'));

  return traces;
}
