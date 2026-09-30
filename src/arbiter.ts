// Gesture arbiter: recognizes click / drag / pinch from raw pointer events.
//
// Arbitration model:
//  - An *interaction* spans first pointerdown .. last pointer removal.
//  - A *generation* bumps every time the active pointer set changes.
//    Gestures are scoped to a generation: drag needs exactly 1 pointer,
//    pinch needs >= 2. Changing the set kills any previewing gesture
//    (with compensation for already-emitted previews).
//  - Per interaction at most ONE final commit among {drag, pinch}.
//    Click commits only if no drag/pinch preview was ever emitted.
//  - lostpointercapture / pointercancel / stale-timeout remove a pointer
//    without committing; a previewing gesture is compensated, never committed.

import { SpaceConverter, dist, type Vec } from './geometry.ts';
import { hitTest, type LaidOutNode } from './layout.ts';
import {
  DEFAULT_THRESHOLDS,
  type GestureEvent,
  type GestureKind,
  type PointerEventLike,
  type Thresholds,
} from './types.ts';

export type ArbiterState = 'idle' | 'tracking' | 'drag-preview' | 'pinch-preview';

interface TrackedPointer {
  id: number;
  pos: Vec;            // canonical space
  downPos: Vec;        // canonical space
  lastTime: number;
}

export interface DispatchResult {
  events: GestureEvent[];
  /** True when the input was dropped (delayed event for a dead pointer, etc.). */
  dropped: boolean;
}

export class GestureArbiter {
  private thresholds: Thresholds;
  private converter: SpaceConverter;
  private sceneRoot: LaidOutNode | null;

  private pointers = new Map<number, TrackedPointer>();
  private interactionId = 0;
  private generation = 0;
  private anchorCentroid: Vec = { x: 0, y: 0 };
  private anchorPairDist = 0;
  private previewing: GestureKind | null = null;
  private committed = false;        // a final drag/pinch commit already happened
  private clickEligible = false;
  private everMultiPointer = false;
  private cancelled = false;        // any cancel/capture-loss in this interaction
  private targetId: string | null = null;
  private lastTime = 0;

  constructor(opts: {
    thresholds?: Partial<Thresholds>;
    converter: SpaceConverter;
    sceneRoot?: LaidOutNode | null;
  }) {
    this.thresholds = { ...DEFAULT_THRESHOLDS, ...(opts.thresholds ?? {}) };
    this.converter = opts.converter;
    this.sceneRoot = opts.sceneRoot ?? null;
  }

  get state(): ArbiterState {
    if (this.pointers.size === 0) return 'idle';
    if (this.previewing === 'drag') return 'drag-preview';
    if (this.previewing === 'pinch') return 'pinch-preview';
    return 'tracking';
  }

  get activePointerCount(): number {
    return this.pointers.size;
  }

  get currentInteractionId(): number {
    return this.interactionId;
  }

  get currentGeneration(): number {
    return this.generation;
  }

  dispatch(ev: PointerEventLike): DispatchResult {
    this.lastTime = Math.max(this.lastTime, ev.time);
    switch (ev.type) {
      case 'pointerdown': return this.onDown(ev);
      case 'pointermove': return this.onMove(ev);
      case 'pointerup': return this.onUp(ev);
      case 'pointercancel':
      case 'lostpointercapture': return this.onCancel(ev);
    }
  }

  /** Drive the stale-pointer watchdog. Expired pointers are dropped like capture loss. */
  tick(now: number): GestureEvent[] {
    this.lastTime = Math.max(this.lastTime, now);
    const out: GestureEvent[] = [];
    for (const p of [...this.pointers.values()]) {
      if (now - p.lastTime > this.thresholds.staleTimeout) {
        out.push(...this.removePointer(p.id, now, 'cancel'));
      }
    }
    return out;
  }

  // ---- internals -------------------------------------------------------

  private canonicalPos(ev: PointerEventLike): Vec | null {
    return this.converter.toCanonical({ x: ev.x, y: ev.y }, ev.space);
  }

  private centroid(): Vec {
    let x = 0, y = 0;
    for (const p of this.pointers.values()) { x += p.pos.x; y += p.pos.y; }
    const n = this.pointers.size || 1;
    return { x: x / n, y: y / n };
  }

  private pairDist(): number {
    const pts = [...this.pointers.values()];
    if (pts.length < 2) return 0;
    return dist(pts[0]!.pos, pts[1]!.pos);
  }

  private reanchor(): void {
    this.anchorCentroid = this.centroid();
    this.anchorPairDist = this.pairDist();
  }

  private makeEvent(kind: GestureKind, phase: GestureEvent['phase'], time: number): GestureEvent {
    const c = this.centroid();
    const scale = this.anchorPairDist > 0 ? this.pairDist() / this.anchorPairDist : 1;
    return {
      kind, phase,
      interactionId: this.interactionId,
      generation: this.generation,
      pointerIds: [...this.pointers.keys()].sort((a, b) => a - b),
      targetId: this.targetId,
      x: c.x, y: c.y,
      dx: c.x - this.anchorCentroid.x,
      dy: c.y - this.anchorCentroid.y,
      scale, time,
    };
  }

  private onDown(ev: PointerEventLike): DispatchResult {
    const pos = this.canonicalPos(ev);
    if (!pos) return { events: [], dropped: true }; // unknown space: cannot track
    const events: GestureEvent[] = [];
    if (this.pointers.size === 0) {
      this.interactionId++;
      this.generation = 1;
      this.committed = false;
      this.cancelled = false;
      this.everMultiPointer = false;
      this.clickEligible = true;
      this.targetId = this.sceneRoot ? (hitTest(this.sceneRoot, pos)?.id ?? null) : null;
    } else {
      // Pointer set changes -> new generation; displace any previewing gesture.
      events.push(...this.displacePreview(ev.time));
      this.generation++;
      this.everMultiPointer = true;
      this.clickEligible = false;
    }
    this.pointers.set(ev.pointerId, { id: ev.pointerId, pos, downPos: pos, lastTime: ev.time });
    this.reanchor();
    return { events, dropped: false };
  }

  private onMove(ev: PointerEventLike): DispatchResult {
    const p = this.pointers.get(ev.pointerId);
    if (!p) return { events: [], dropped: true }; // delayed event for a dead pointer
    const pos = this.canonicalPos(ev);
    if (!pos) {
      // Coordinate space we cannot convert: hard re-anchor, preview compensated.
      const events = this.displacePreview(ev.time);
      p.lastTime = ev.time;
      this.reanchor();
      return { events, dropped: false };
    }
    p.pos = pos;
    p.lastTime = ev.time;
    if (dist(p.pos, p.downPos) > this.thresholds.clickSlop) this.clickEligible = false;

    const events: GestureEvent[] = [];
    if (this.committed) return { events, dropped: false }; // interaction already finalized

    if (this.pointers.size === 1) {
      if (this.previewing === 'drag') {
        events.push(this.makeEvent('drag', 'preview', ev.time));
      } else if (!this.previewing && dist(this.centroid(), this.anchorCentroid) > this.thresholds.drag) {
        this.previewing = 'drag';
        this.clickEligible = false;
        events.push(this.makeEvent('drag', 'preview', ev.time));
      }
    } else if (this.pointers.size >= 2) {
      const scale = this.anchorPairDist > 0 ? this.pairDist() / this.anchorPairDist : 1;
      const pan = dist(this.centroid(), this.anchorCentroid);
      if (this.previewing === 'pinch') {
        events.push(this.makeEvent('pinch', 'preview', ev.time));
      } else if (!this.previewing &&
                 (Math.abs(scale - 1) > this.thresholds.pinchScale || pan > this.thresholds.pinchPan)) {
        this.previewing = 'pinch';
        events.push(this.makeEvent('pinch', 'preview', ev.time));
      }
    }
    return { events, dropped: false };
  }

  private onUp(ev: PointerEventLike): DispatchResult {
    if (!this.pointers.has(ev.pointerId)) return { events: [], dropped: true };
    const pos = this.canonicalPos(ev);
    if (pos) this.pointers.get(ev.pointerId)!.pos = pos;
    const events = this.removePointer(ev.pointerId, ev.time, 'up');
    return { events, dropped: false };
  }

  private onCancel(ev: PointerEventLike): DispatchResult {
    if (!this.pointers.has(ev.pointerId)) return { events: [], dropped: true };
    this.cancelled = true;
    this.clickEligible = false;
    const events = this.removePointer(ev.pointerId, ev.time, 'cancel');
    return { events, dropped: false };
  }

  /** Emit compensate for a previewing gesture displaced by a set/space change. */
  private displacePreview(time: number): GestureEvent[] {
    if (!this.previewing) return [];
    const kind = this.previewing;
    this.previewing = null;
    return [this.makeEvent(kind, 'compensate', time)];
  }

  private removePointer(id: number, time: number, cause: 'up' | 'cancel'): GestureEvent[] {
    const events: GestureEvent[] = [];
    if (this.previewing) {
      const kind = this.previewing;
      this.previewing = null;
      if (cause === 'up' && !this.committed) {
        // Final, mutually exclusive commit.
        events.push(this.makeEvent(kind, 'commit', time));
        this.committed = true;
      } else {
        events.push(this.makeEvent(kind, 'compensate', time));
      }
    }
    // Click is decided while the departing pointer is still tracked so the
    // event carries real coordinates.
    const isLastPointer = this.pointers.size === 1;
    if (isLastPointer && cause === 'up' && !this.previewing &&
        this.clickEligible && !this.everMultiPointer && !this.committed && !this.cancelled) {
      events.push(this.makeEvent('click', 'commit', time));
    }
    this.pointers.delete(id);
    if (this.pointers.size === 0) return events;
    // Set changed: next generation, fresh anchor.
    this.generation++;
    this.reanchor();
    return events;
  }
}
