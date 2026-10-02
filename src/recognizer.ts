import type {
  CompensateOutput,
  GestureOutput,
  PointerInput,
} from './types.ts';

export interface RecognizerOptions {
  /** Movement budget in px. dist > slop promotes a click candidate to drag. */
  tapSlop?: number;
  /** Duration budget in ms. A click must be released within this. */
  tapTimeout?: number;
  /** Optional fixture-driven hit test, resolved on the first pointerdown. */
  hitTest?: (x: number, y: number) => string | undefined;
}

type Phase = 'idle' | 'possible' | 'drag' | 'pinch' | 'settled';

interface Tracked {
  id: number;
  startX: number;
  startY: number;
  lastX: number;
  lastY: number;
  lastT: number;
  epoch: number;
}

const DEFAULT_SLOP = 10;
const DEFAULT_TIMEOUT = 300;
const EPS = 1e-9;

/**
 * Gesture arbiter over a set of active pointers.
 *
 * Generation: each time the recognizer leaves `idle` a new generation starts;
 * every output carries its `gen`. Per generation at most one `commit` is
 * emitted, so mutually exclusive gestures (click / drag / pinch) can never
 * both commit from the same pointer session.
 *
 * Pointer-set arbitration:
 *   1 pointer  -> click candidate, promoted to drag once slop is exceeded.
 *   2 pointers -> pinch; an active drag is compensated + cancelled first.
 *   0 pointers -> generation ends, recognizer returns to idle (a lost pointer
 *                 can therefore never keep a gesture active forever).
 */
export class GestureRecognizer {
  private readonly slop: number;
  private readonly timeout: number;
  private readonly hit?: (x: number, y: number) => string | undefined;

  private phase: Phase = 'idle';
  private gen = 0;
  private readonly pointers = new Map<number, Tracked>();
  private targetId: string | undefined = undefined;
  private genStartT = 0;

  // Accumulators for the current gesture segment (drag or pinch).
  private segDx = 0;
  private segDy = 0;
  private segScale = 1;
  private segPreviews = false;

  // Pinch reference frame (incremental scale/centroid baseline).
  private refValid = false;
  private refDist = 0;
  private refCx = 0;
  private refCy = 0;

  constructor(opts: RecognizerOptions = {}) {
    this.slop = opts.tapSlop ?? DEFAULT_SLOP;
    this.timeout = opts.tapTimeout ?? DEFAULT_TIMEOUT;
    this.hit = opts.hitTest;
  }

  getPhase(): Phase {
    return this.phase;
  }

  isIdle(): boolean {
    return this.phase === 'idle';
  }

  generation(): number {
    return this.gen;
  }

  activePointers(): number {
    return this.pointers.size;
  }

  handle(ev: PointerInput): GestureOutput[] {
    const epoch = ev.epoch ?? 0;
    const tracked = this.pointers.get(ev.pointerId);

    if (ev.type === 'down') {
      if (tracked) return [this.ignored(ev, 'duplicate-down')];
      return this.onDown(ev, epoch);
    }
    if (!tracked) return [this.ignored(ev, 'unknown-pointer')];
    if (ev.t < tracked.lastT) return [this.ignored(ev, 'out-of-order')];

    switch (ev.type) {
      case 'move':
        return this.onMove(ev, epoch, tracked);
      case 'up':
        return this.onUp(ev, tracked);
      case 'cancel':
      case 'lostcapture':
        return this.onLoss(ev, tracked);
    }
  }

  private ignored(ev: PointerInput, reason: string): GestureOutput {
    return { kind: 'ignored', reason, inputType: ev.type, pointerId: ev.pointerId };
  }

  private track(ev: PointerInput, epoch: number): void {
    this.pointers.set(ev.pointerId, {
      id: ev.pointerId,
      startX: ev.x,
      startY: ev.y,
      lastX: ev.x,
      lastY: ev.y,
      lastT: ev.t,
      epoch,
    });
  }

  private resetSegment(): void {
    this.segDx = 0;
    this.segDy = 0;
    this.segScale = 1;
    this.segPreviews = false;
  }

  private compensate(gesture: 'drag' | 'pinch'): CompensateOutput {
    return {
      kind: 'compensate',
      gesture,
      gen: this.gen,
      dx: -this.segDx,
      dy: -this.segDy,
      scale: 1 / this.segScale,
    };
  }

  private onDown(ev: PointerInput, epoch: number): GestureOutput[] {
    if (this.phase === 'settled') return [this.ignored(ev, 'session-closed')];
    if (this.phase === 'pinch') return [this.ignored(ev, 'max-pointers')];

    if (this.phase === 'idle') {
      this.gen += 1;
      this.phase = 'possible';
      this.genStartT = ev.t;
      this.targetId = this.hit?.(ev.x, ev.y);
      this.resetSegment();
      this.track(ev, epoch);
      return [];
    }

    // possible | drag: a second pointer joins -> switch to pinch per rules.
    const out: GestureOutput[] = [];
    if (this.phase === 'drag') {
      if (this.segPreviews) out.push(this.compensate('drag'));
      out.push({ kind: 'cancel', gesture: 'drag', gen: this.gen, reason: 'superseded-by-pinch' });
    } else {
      out.push({ kind: 'cancel', gesture: 'click', gen: this.gen, reason: 'superseded-by-pinch' });
    }
    this.track(ev, epoch);
    this.phase = 'pinch';
    this.resetSegment();
    this.baselinePinchIfAligned();
    return out;
  }

  private onMove(ev: PointerInput, epoch: number, p: Tracked): GestureOutput[] {
    if (epoch !== p.epoch) {
      // Coordinate space changed: re-baseline instead of emitting a jump.
      p.startX = ev.x;
      p.startY = ev.y;
      p.lastX = ev.x;
      p.lastY = ev.y;
      p.lastT = ev.t;
      p.epoch = epoch;
      if (this.phase === 'pinch') {
        this.refValid = false;
        this.baselinePinchIfAligned();
      }
      return [];
    }

    const dx = ev.x - p.lastX;
    const dy = ev.y - p.lastY;
    p.lastX = ev.x;
    p.lastY = ev.y;
    p.lastT = ev.t;

    switch (this.phase) {
      case 'possible': {
        const totalX = ev.x - p.startX;
        const totalY = ev.y - p.startY;
        if (Math.hypot(totalX, totalY) > this.slop) {
          this.phase = 'drag';
          this.resetSegment();
          this.segDx = totalX;
          this.segDy = totalY;
          this.segPreviews = true;
          return [
            { kind: 'cancel', gesture: 'click', gen: this.gen, reason: 'slop-exceeded' },
            { kind: 'preview', gesture: 'drag', gen: this.gen, dx: totalX, dy: totalY, scale: 1, cx: ev.x, cy: ev.y },
          ];
        }
        return [];
      }
      case 'drag': {
        this.segDx += dx;
        this.segDy += dy;
        this.segPreviews = true;
        return [{ kind: 'preview', gesture: 'drag', gen: this.gen, dx, dy, scale: 1, cx: ev.x, cy: ev.y }];
      }
      case 'pinch': {
        if (!this.refValid) {
          this.baselinePinchIfAligned();
          return [];
        }
        const other = this.other(p.id);
        if (!other) return [];
        const cx = (p.lastX + other.lastX) / 2;
        const cy = (p.lastY + other.lastY) / 2;
        const dist = Math.hypot(p.lastX - other.lastX, p.lastY - other.lastY);
        const scale = this.refDist > EPS ? dist / this.refDist : 1;
        const ddx = cx - this.refCx;
        const ddy = cy - this.refCy;
        this.refDist = dist;
        this.refCx = cx;
        this.refCy = cy;
        this.segDx += ddx;
        this.segDy += ddy;
        this.segScale *= scale;
        this.segPreviews = true;
        return [{ kind: 'preview', gesture: 'pinch', gen: this.gen, dx: ddx, dy: ddy, scale, cx, cy }];
      }
      default:
        return []; // settled: remaining pointers are inert
    }
  }

  private onUp(ev: PointerInput, p: Tracked): GestureOutput[] {
    const out: GestureOutput[] = [];
    const duration = ev.t - this.genStartT;

    if (this.phase === 'possible') {
      const dist = Math.hypot(ev.x - p.startX, ev.y - p.startY);
      if (dist <= this.slop && duration <= this.timeout) {
        out.push({ kind: 'commit', gesture: 'click', gen: this.gen, dx: 0, dy: 0, scale: 1, duration, targetId: this.targetId });
      } else {
        const reason = duration > this.timeout ? 'timeout' : 'moved';
        out.push({ kind: 'cancel', gesture: 'click', gen: this.gen, reason });
      }
    } else if (this.phase === 'drag') {
      out.push({ kind: 'commit', gesture: 'drag', gen: this.gen, dx: this.segDx, dy: this.segDy, scale: 1, duration, targetId: this.targetId });
    } else if (this.phase === 'pinch') {
      if (this.segPreviews) {
        out.push({ kind: 'commit', gesture: 'pinch', gen: this.gen, dx: this.segDx, dy: this.segDy, scale: this.segScale, duration, targetId: this.targetId });
      } else {
        out.push({ kind: 'cancel', gesture: 'pinch', gen: this.gen, reason: 'no-movement' });
      }
      this.phase = 'settled'; // the remaining pointer stays inert: no click after pinch
    }

    this.pointers.delete(ev.pointerId);
    if (this.pointers.size === 0) {
      this.phase = 'idle';
      this.targetId = undefined;
    }
    return out;
  }

  private onLoss(ev: PointerInput, _p: Tracked): GestureOutput[] {
    const out: GestureOutput[] = [];

    if (this.phase === 'possible') {
      out.push({ kind: 'cancel', gesture: 'click', gen: this.gen, reason: 'pointer-lost' });
    } else if (this.phase === 'drag') {
      if (this.segPreviews) out.push(this.compensate('drag'));
      out.push({ kind: 'cancel', gesture: 'drag', gen: this.gen, reason: 'pointer-lost' });
    } else if (this.phase === 'pinch') {
      if (this.segPreviews) out.push(this.compensate('pinch'));
      out.push({ kind: 'cancel', gesture: 'pinch', gen: this.gen, reason: 'pointer-lost' });
      this.phase = 'settled';
    }

    this.pointers.delete(ev.pointerId);
    if (this.pointers.size === 0) {
      this.phase = 'idle';
      this.targetId = undefined;
    }
    return out;
  }

  private other(id: number): Tracked | undefined {
    for (const p of this.pointers.values()) {
      if (p.id !== id) return p;
    }
    return undefined;
  }

  private baselinePinchIfAligned(): void {
    const ps = [...this.pointers.values()];
    if (ps.length !== 2 || ps[0].epoch !== ps[1].epoch) {
      this.refValid = false;
      return;
    }
    this.refDist = Math.hypot(ps[0].lastX - ps[1].lastX, ps[0].lastY - ps[1].lastY);
    this.refCx = (ps[0].lastX + ps[1].lastX) / 2;
    this.refCy = (ps[0].lastY + ps[1].lastY) / 2;
    this.refValid = true;
  }
}
