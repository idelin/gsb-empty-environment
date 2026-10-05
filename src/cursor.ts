import type { Segment, TraceEvent } from './types.js';

export type ObservablePred = (action: string) => boolean;

function pushRing(ring: TraceEvent[], cap: number, e: TraceEvent): void {
  if (cap <= 0) return;
  ring.push(e);
  while (ring.length > cap) ring.shift();
}

function feedRingTail(ring: TraceEvent[], cap: number, body: TraceEvent[], remaining: number): void {
  const tailLen = Math.min(cap, remaining, body.length);
  for (let i = body.length - tailLen; i < body.length; i++) pushRing(ring, cap, body[i]);
}

export class SegmentCursor {
  segments: Segment[];
  seg: number = 0;
  pos: number = 0;
  iter: number = 0;
  bodyPos: number = 0;
  consumed: number = 0;
  loopCache: Map<number, boolean> = new Map();

  constructor(segments: Segment[]) {
    this.segments = segments;
  }

  clone(): SegmentCursor {
    const c = new SegmentCursor(this.segments);
    c.seg = this.seg;
    c.pos = this.pos;
    c.iter = this.iter;
    c.bodyPos = this.bodyPos;
    c.consumed = this.consumed;
    c.loopCache = this.loopCache;
    return c;
  }

  advanceSegment(): void {
    this.seg++;
    this.pos = 0;
    this.iter = 0;
    this.bodyPos = 0;
  }

  peek(): TraceEvent | null {
    for (;;) {
      if (this.seg >= this.segments.length) return null;
      const s = this.segments[this.seg];
      if (s.kind === 'run') {
        if (this.pos < s.events.length) return s.events[this.pos];
      } else if (s.body.length > 0 && this.iter < s.times) {
        return s.body[this.bodyPos];
      }
      this.advanceSegment();
    }
  }

  next(): TraceEvent | null {
    const e = this.peek();
    if (e === null) return null;
    const s = this.segments[this.seg];
    if (s.kind === 'run') {
      this.pos++;
    } else {
      this.bodyPos++;
      if (this.bodyPos >= s.body.length) {
        this.bodyPos = 0;
        this.iter++;
      }
    }
    this.consumed++;
    return e;
  }

  bodyHasObservable(segIndex: number, body: TraceEvent[], pred: ObservablePred): boolean {
    const cached = this.loopCache.get(segIndex);
    if (cached !== undefined) return cached;
    let has = false;
    for (const e of body) {
      if (pred(e.action)) {
        has = true;
        break;
      }
    }
    this.loopCache.set(segIndex, has);
    return has;
  }

  skipInternal(pred: ObservablePred, ring: TraceEvent[], ringCap: number): number {
    let skipped = 0;
    for (;;) {
      if (this.seg >= this.segments.length) break;
      const s = this.segments[this.seg];
      if (s.kind === 'run') {
        if (this.pos >= s.events.length) {
          this.advanceSegment();
          continue;
        }
        const e = s.events[this.pos];
        if (pred(e.action)) break;
        this.pos++;
        this.consumed++;
        skipped++;
        pushRing(ring, ringCap, e);
        continue;
      }
      if (s.body.length === 0 || this.iter >= s.times) {
        this.advanceSegment();
        continue;
      }
      if (!this.bodyHasObservable(this.seg, s.body, pred)) {
        const remaining = s.body.length * (s.times - this.iter) - this.bodyPos;
        this.consumed += remaining;
        skipped += remaining;
        feedRingTail(ring, ringCap, s.body, remaining);
        this.advanceSegment();
        continue;
      }
      const e = s.body[this.bodyPos];
      if (pred(e.action)) break;
      this.bodyPos++;
      if (this.bodyPos >= s.body.length) {
        this.bodyPos = 0;
        this.iter++;
      }
      this.consumed++;
      skipped++;
      pushRing(ring, ringCap, e);
    }
    return skipped;
  }
}

export function countObservables(events: TraceEvent[], pred: ObservablePred, from: number, to: number): number {
  let n = 0;
  for (let i = from; i < to; i++) {
    if (pred(events[i].action)) n++;
  }
  return n;
}

export function countRemainingObservables(cursor: SegmentCursor, pred: ObservablePred): number {
  let total = 0;
  for (let s = cursor.seg; s < cursor.segments.length; s++) {
    const seg = cursor.segments[s];
    if (seg.kind === 'run') {
      const from = s === cursor.seg ? cursor.pos : 0;
      total += countObservables(seg.events, pred, from, seg.events.length);
    } else {
      const bodyTotal = countObservables(seg.body, pred, 0, seg.body.length);
      if (s === cursor.seg) {
        const before = countObservables(seg.body, pred, 0, cursor.bodyPos);
        total += bodyTotal * (seg.times - cursor.iter) - before;
      } else {
        total += bodyTotal * seg.times;
      }
    }
  }
  return total;
}

export function totalEvents(segments: Segment[]): number {
  let total = 0;
  for (const s of segments) {
    total += s.kind === 'run' ? s.events.length : s.body.length * s.times;
  }
  return total;
}
