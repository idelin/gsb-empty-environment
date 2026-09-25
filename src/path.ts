import { applyToPoint } from './matrix.ts';
import type { Matrix } from './matrix.ts';

export interface Point {
  x: number;
  y: number;
}

type Segment =
  | { kind: 'line'; to: Point }
  | { kind: 'quad'; control: Point; to: Point }
  | { kind: 'cubic'; control1: Point; control2: Point; to: Point };

interface Contour {
  start: Point;
  segments: Segment[];
  closed: boolean;
}

const FLATNESS = 0.02;
const MAX_DEPTH = 16;

function distanceToChord(p: Point, a: Point, b: Point): number {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.hypot(dx, dy);
  if (len < 1e-12) return Math.hypot(p.x - a.x, p.y - a.y);
  return Math.abs(dx * (a.y - p.y) - (a.x - p.x) * dy) / len;
}

function midpoint(p: Point, q: Point): Point {
  return { x: (p.x + q.x) / 2, y: (p.y + q.y) / 2 };
}

function flattenQuad(p0: Point, c: Point, p1: Point, out: Point[], depth: number): void {
  if (depth >= MAX_DEPTH || distanceToChord(c, p0, p1) <= FLATNESS) {
    out.push(p1);
    return;
  }
  const p0c = midpoint(p0, c);
  const cp1 = midpoint(c, p1);
  const mid = midpoint(p0c, cp1);
  flattenQuad(p0, p0c, mid, out, depth + 1);
  flattenQuad(mid, cp1, p1, out, depth + 1);
}

function flattenCubic(p0: Point, c1: Point, c2: Point, p1: Point, out: Point[], depth: number): void {
  const flat = distanceToChord(c1, p0, p1) <= FLATNESS && distanceToChord(c2, p0, p1) <= FLATNESS;
  if (depth >= MAX_DEPTH || flat) {
    out.push(p1);
    return;
  }
  const p0c1 = midpoint(p0, c1);
  const c1c2 = midpoint(c1, c2);
  const c2p1 = midpoint(c2, p1);
  const p0c1c2 = midpoint(p0c1, c1c2);
  const c1c2p1 = midpoint(c1c2, c2p1);
  const mid = midpoint(p0c1c2, c1c2p1);
  flattenCubic(p0, p0c1, p0c1c2, mid, out, depth + 1);
  flattenCubic(mid, c1c2p1, c2p1, p1, out, depth + 1);
}

export class PathBuilder {
  private contours: Contour[] = [];
  private cursor: Point = { x: 0, y: 0 };

  moveTo(x: number, y: number): this {
    this.contours.push({ start: { x, y }, segments: [], closed: false });
    this.cursor = { x, y };
    return this;
  }

  lineTo(x: number, y: number): this {
    this.active().segments.push({ kind: 'line', to: { x, y } });
    this.cursor = { x, y };
    return this;
  }

  quadraticTo(cx: number, cy: number, x: number, y: number): this {
    this.active().segments.push({ kind: 'quad', control: { x: cx, y: cy }, to: { x, y } });
    this.cursor = { x, y };
    return this;
  }

  cubicTo(c1x: number, c1y: number, c2x: number, c2y: number, x: number, y: number): this {
    this.active().segments.push({
      kind: 'cubic',
      control1: { x: c1x, y: c1y },
      control2: { x: c2x, y: c2y },
      to: { x, y },
    });
    this.cursor = { x, y };
    return this;
  }

  closePath(): this {
    this.active().closed = true;
    return this;
  }

  rect(x: number, y: number, width: number, height: number): this {
    return this.moveTo(x, y)
      .lineTo(x + width, y)
      .lineTo(x + width, y + height)
      .lineTo(x, y + height)
      .closePath();
  }

  private active(): Contour {
    const contour = this.contours[this.contours.length - 1];
    if (!contour) throw new Error('path is empty: call moveTo first');
    return contour;
  }

  flatten(matrix: Matrix): Point[][] {
    const out: Point[][] = [];
    for (const contour of this.contours) {
      const points: Point[] = [applyToPoint(matrix, contour.start.x, contour.start.y)];
      let pen = contour.start;
      for (const seg of contour.segments) {
        if (seg.kind === 'line') {
          points.push(applyToPoint(matrix, seg.to.x, seg.to.y));
        } else if (seg.kind === 'quad') {
          flattenQuad(
            applyToPoint(matrix, pen.x, pen.y),
            applyToPoint(matrix, seg.control.x, seg.control.y),
            applyToPoint(matrix, seg.to.x, seg.to.y),
            points,
            0,
          );
        } else {
          flattenCubic(
            applyToPoint(matrix, pen.x, pen.y),
            applyToPoint(matrix, seg.control1.x, seg.control1.y),
            applyToPoint(matrix, seg.control2.x, seg.control2.y),
            applyToPoint(matrix, seg.to.x, seg.to.y),
            points,
            0,
          );
        }
        pen = seg.to;
      }
      if (points.length >= 2) out.push(points);
    }
    return out;
  }
}
