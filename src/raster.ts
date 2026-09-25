import type { Point } from './path.ts';

export interface Mask {
  width: number;
  height: number;
  data: Float32Array;
}

export interface Region {
  x: number;
  y: number;
  width: number;
  height: number;
}

export function createMask(width: number, height: number, fill = 0): Mask {
  const data = new Float32Array(width * height);
  if (fill !== 0) data.fill(fill);
  return { width, height, data };
}

function signedArea(poly: Point[]): number {
  let sum = 0;
  for (let i = 0; i < poly.length; i++) {
    const p = poly[i];
    const q = poly[(i + 1) % poly.length];
    sum += p.x * q.y - q.x * p.y;
  }
  return sum / 2;
}

interface HalfPlane {
  inside(p: Point): boolean;
  intersect(p: Point, q: Point): Point;
}

function crossX(p: Point, q: Point, x: number): Point {
  const t = (x - p.x) / (q.x - p.x);
  return { x, y: p.y + t * (q.y - p.y) };
}

function crossY(p: Point, q: Point, y: number): Point {
  const t = (y - p.y) / (q.y - p.y);
  return { x: p.x + t * (q.x - p.x), y };
}

function clipHalfPlane(poly: Point[], plane: HalfPlane): Point[] {
  const out: Point[] = [];
  for (let i = 0; i < poly.length; i++) {
    const p = poly[i];
    const q = poly[(i + 1) % poly.length];
    const pIn = plane.inside(p);
    const qIn = plane.inside(q);
    if (qIn) {
      if (!pIn) out.push(plane.intersect(p, q));
      out.push(q);
    } else if (pIn) {
      out.push(plane.intersect(p, q));
    }
  }
  return out;
}

function clipToPixel(poly: Point[], px: number, py: number): Point[] {
  const planes: HalfPlane[] = [
    { inside: (p) => p.x >= px, intersect: (p, q) => crossX(p, q, px) },
    { inside: (p) => p.x <= px + 1, intersect: (p, q) => crossX(p, q, px + 1) },
    { inside: (p) => p.y >= py, intersect: (p, q) => crossY(p, q, py) },
    { inside: (p) => p.y <= py + 1, intersect: (p, q) => crossY(p, q, py + 1) },
  ];
  let result = poly;
  for (const plane of planes) {
    result = clipHalfPlane(result, plane);
    if (result.length === 0) break;
  }
  return result;
}

export function fillPath(contours: Point[][], width: number, height: number): Mask {
  const mask = createMask(width, height);
  for (const contour of contours) {
    if (contour.length < 3) continue;
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const p of contour) {
      if (p.x < minX) minX = p.x;
      if (p.y < minY) minY = p.y;
      if (p.x > maxX) maxX = p.x;
      if (p.y > maxY) maxY = p.y;
    }
    const x0 = Math.max(0, Math.floor(minX));
    const y0 = Math.max(0, Math.floor(minY));
    const x1 = Math.min(width - 1, Math.ceil(maxX) - 1);
    const y1 = Math.min(height - 1, Math.ceil(maxY) - 1);
    for (let py = y0; py <= y1; py++) {
      for (let px = x0; px <= x1; px++) {
        const clipped = clipToPixel(contour, px, py);
        if (clipped.length < 3) continue;
        mask.data[py * width + px] += signedArea(clipped);
      }
    }
  }
  for (let i = 0; i < mask.data.length; i++) {
    const v = mask.data[i];
    mask.data[i] = v < 0 ? 0 : v > 1 ? 1 : v;
  }
  return mask;
}

function assertSameSize(a: Mask, b: Mask): void {
  if (a.width !== b.width || a.height !== b.height) {
    throw new Error(`mask size mismatch: ${a.width}x${a.height} vs ${b.width}x${b.height}`);
  }
}

function combine(a: Mask, b: Mask, fn: (x: number, y: number) => number): Mask {
  assertSameSize(a, b);
  const out = createMask(a.width, a.height);
  for (let i = 0; i < out.data.length; i++) {
    const v = fn(a.data[i], b.data[i]);
    out.data[i] = v < 0 ? 0 : v > 1 ? 1 : v;
  }
  return out;
}

export function maskUnion(a: Mask, b: Mask): Mask {
  return combine(a, b, (x, y) => x + y - x * y);
}

export function maskIntersect(a: Mask, b: Mask): Mask {
  return combine(a, b, (x, y) => x * y);
}

export function maskDifference(a: Mask, b: Mask): Mask {
  return combine(a, b, (x, y) => x * (1 - y));
}

export function invertMask(mask: Mask, region: Region): Mask {
  const out = createMask(mask.width, mask.height);
  const x0 = Math.max(0, Math.round(region.x));
  const y0 = Math.max(0, Math.round(region.y));
  const x1 = Math.min(mask.width, Math.round(region.x + region.width));
  const y1 = Math.min(mask.height, Math.round(region.y + region.height));
  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) {
      const i = y * mask.width + x;
      out.data[i] = 1 - mask.data[i];
    }
  }
  return out;
}

export function applyMask(base: Mask, ...masks: Mask[]): Mask {
  const out = createMask(base.width, base.height);
  out.data.set(base.data);
  for (const mask of masks) {
    assertSameSize(out, mask);
    for (let i = 0; i < out.data.length; i++) {
      out.data[i] *= mask.data[i];
    }
  }
  return out;
}
