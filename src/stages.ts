import { apply, invert, isSingular, scaling, translation } from './affine.ts';
import type { Affine } from './affine.ts';
import { FloatImage } from './image.ts';
import {
  fullRect,
  intersectConvex,
  intersectRect,
  intersectionArea,
  pointInConvex,
  polygonSignedArea,
  unionRect,
} from './geometry.ts';
import type { Pt, Rect } from './geometry.ts';

export type FilterKind = 'copy' | 'bilinear' | 'area';

export class CancelledError extends Error {
  constructor() {
    super('render cancelled');
    this.name = 'CancelledError';
  }
}

export type AbortCheck = () => boolean;

export class CancelToken {
  cancelled = false;
  readonly check: AbortCheck = () => this.cancelled;
}

function isIntegerTranslate(m: Affine): boolean {
  return (
    m.a === 1 &&
    m.b === 0 &&
    m.c === 0 &&
    m.d === 1 &&
    Number.isInteger(m.e) &&
    Number.isInteger(m.f)
  );
}

export class Stage {
  readonly srcW: number;
  readonly srcH: number;
  readonly dstW: number;
  readonly dstH: number;
  readonly forward: Affine;
  readonly inverse: Affine;
  readonly filter: FilterKind;
  readonly srcClip: Pt[];
  rowsRendered = 0;

  constructor(
    srcW: number,
    srcH: number,
    dstW: number,
    dstH: number,
    forward: Affine,
    filter: FilterKind,
    srcClip?: Pt[],
  ) {
    if (isSingular(forward)) {
      throw new Error('stage transform is singular and was rejected');
    }
    if (!(srcW > 0 && srcH > 0 && dstW > 0 && dstH > 0)) {
      throw new Error('stage sizes must be positive');
    }
    this.srcW = srcW;
    this.srcH = srcH;
    this.dstW = dstW;
    this.dstH = dstH;
    this.forward = forward;
    this.inverse = invert(forward);
    if (filter === 'copy' && !isIntegerTranslate(this.inverse)) {
      throw new Error('copy filter requires an integer translation');
    }
    this.filter = filter;
    this.srcClip = srcClip ?? [
      [0, 0],
      [srcW, 0],
      [srcW, srcH],
      [0, srcH],
    ];
  }

  get clipEmpty(): boolean {
    return this.srcClip.length < 3;
  }

  static crop(srcW: number, srcH: number, x: number, y: number, w: number, h: number): Stage {
    if (!Number.isInteger(x) || !Number.isInteger(y)) {
      throw new Error('crop origin must be integer pixels');
    }
    return new Stage(srcW, srcH, w, h, translation(-x, -y), 'copy');
  }

  static affine(
    srcW: number,
    srcH: number,
    dstW: number,
    dstH: number,
    forward: Affine,
    filter: FilterKind,
  ): Stage {
    return new Stage(srcW, srcH, dstW, dstH, forward, filter);
  }

  static scale(srcW: number, srcH: number, sx: number, sy: number): Stage {
    const dstW = Math.max(1, Math.round(srcW * sx));
    const dstH = Math.max(1, Math.round(srcH * sy));
    const fwd = scaling(dstW / srcW, dstH / srcH);
    const filter: FilterKind = dstW <= srcW && dstH <= srcH ? 'area' : 'bilinear';
    return new Stage(srcW, srcH, dstW, dstH, fwd, filter);
  }

  static translate(srcW: number, srcH: number, dx: number, dy: number): Stage {
    const fwd = translation(dx, dy);
    const filter: FilterKind =
      Number.isInteger(dx) && Number.isInteger(dy) ? 'copy' : 'bilinear';
    return new Stage(srcW, srcH, srcW, srcH, fwd, filter);
  }

  isCopy(): boolean {
    return this.filter === 'copy';
  }

  render(
    src: FloatImage,
    dst: FloatImage,
    region: Rect,
    shouldAbort?: AbortCheck,
  ): void {
    const r = intersectRect(region, fullRect(this.dstW, this.dstH));
    if (!r) return;
    if (this.clipEmpty) {
      this.zeroFill(dst, r, shouldAbort);
      return;
    }
    switch (this.filter) {
      case 'copy':
        this.renderCopy(src, dst, r, shouldAbort);
        break;
      case 'bilinear':
        this.renderBilinear(src, dst, r, shouldAbort);
        break;
      case 'area':
        this.renderArea(src, dst, r, shouldAbort);
        break;
    }
  }

  private checkAbort(shouldAbort?: AbortCheck): void {
    if (shouldAbort && shouldAbort()) throw new CancelledError();
  }

  private zeroFill(dst: FloatImage, r: Rect, shouldAbort?: AbortCheck): void {
    for (let j = r.y; j < r.y + r.h; j++) {
      this.checkAbort(shouldAbort);
      for (let i = r.x; i < r.x + r.w; i++) {
        const o = (j * this.dstW + i) * 4;
        dst.data[o] = 0;
        dst.data[o + 1] = 0;
        dst.data[o + 2] = 0;
        dst.data[o + 3] = 0;
      }
      this.rowsRendered++;
    }
  }

  private renderCopy(src: FloatImage, dst: FloatImage, r: Rect, shouldAbort?: AbortCheck): void {
    const tx = this.inverse.e;
    const ty = this.inverse.f;
    for (let j = r.y; j < r.y + r.h; j++) {
      this.checkAbort(shouldAbort);
      for (let i = r.x; i < r.x + r.w; i++) {
        const sx = i + tx;
        const sy = j + ty;
        const o = (j * this.dstW + i) * 4;
        if (pointInConvex(this.srcClip, sx + 0.5, sy + 0.5)) {
          const p = (sy * this.srcW + sx) * 4;
          dst.data[o] = src.data[p];
          dst.data[o + 1] = src.data[p + 1];
          dst.data[o + 2] = src.data[p + 2];
          dst.data[o + 3] = src.data[p + 3];
        } else {
          dst.data[o] = 0;
          dst.data[o + 1] = 0;
          dst.data[o + 2] = 0;
          dst.data[o + 3] = 0;
        }
      }
      this.rowsRendered++;
    }
  }

  private renderBilinear(src: FloatImage, dst: FloatImage, r: Rect, shouldAbort?: AbortCheck): void {
    for (let j = r.y; j < r.y + r.h; j++) {
      this.checkAbort(shouldAbort);
      for (let i = r.x; i < r.x + r.w; i++) {
        const [px, py] = apply(this.inverse, i + 0.5, j + 0.5);
        const gx = px - 0.5;
        const gy = py - 0.5;
        const x0 = Math.floor(gx);
        const y0 = Math.floor(gy);
        const tx = gx - x0;
        const ty = gy - y0;
        let cr = 0;
        let cg = 0;
        let cb = 0;
        let ca = 0;
        for (let k = 0; k < 4; k++) {
          const sx = x0 + (k & 1);
          const sy = y0 + (k >> 1);
          const wx = k & 1 ? tx : 1 - tx;
          const wy = k >> 1 ? ty : 1 - ty;
          const w = wx * wy;
          if (w === 0) continue;
          if (!pointInConvex(this.srcClip, sx + 0.5, sy + 0.5)) continue;
          const p = (sy * this.srcW + sx) * 4;
          cr += w * src.data[p];
          cg += w * src.data[p + 1];
          cb += w * src.data[p + 2];
          ca += w * src.data[p + 3];
        }
        const o = (j * this.dstW + i) * 4;
        dst.data[o] = cr;
        dst.data[o + 1] = cg;
        dst.data[o + 2] = cb;
        dst.data[o + 3] = ca;
      }
      this.rowsRendered++;
    }
  }

  private renderArea(src: FloatImage, dst: FloatImage, r: Rect, shouldAbort?: AbortCheck): void {
    for (let j = r.y; j < r.y + r.h; j++) {
      this.checkAbort(shouldAbort);
      for (let i = r.x; i < r.x + r.w; i++) {
        const footprint: Pt[] = [
          apply(this.inverse, i, j),
          apply(this.inverse, i + 1, j),
          apply(this.inverse, i + 1, j + 1),
          apply(this.inverse, i, j + 1),
        ];
        const den = Math.abs(polygonSignedArea(footprint));
        const poly = intersectConvex(footprint, this.srcClip);
        let minx = Infinity;
        let miny = Infinity;
        let maxx = -Infinity;
        let maxy = -Infinity;
        for (const p of poly) {
          minx = Math.min(minx, p[0]);
          miny = Math.min(miny, p[1]);
          maxx = Math.max(maxx, p[0]);
          maxy = Math.max(maxy, p[1]);
        }
        const x0 = Math.max(0, Math.floor(minx));
        const y0 = Math.max(0, Math.floor(miny));
        const x1 = Math.min(this.srcW - 1, Math.ceil(maxx) - 1);
        const y1 = Math.min(this.srcH - 1, Math.ceil(maxy) - 1);
        let cr = 0;
        let cg = 0;
        let cb = 0;
        let ca = 0;
        for (let sy = y0; sy <= y1; sy++) {
          for (let sx = x0; sx <= x1; sx++) {
            const sq: Pt[] = [
              [sx, sy],
              [sx + 1, sy],
              [sx + 1, sy + 1],
              [sx, sy + 1],
            ];
            const w = intersectionArea(sq, poly);
            if (w <= 0) continue;
            const p = (sy * this.srcW + sx) * 4;
            cr += w * src.data[p];
            cg += w * src.data[p + 1];
            cb += w * src.data[p + 2];
            ca += w * src.data[p + 3];
          }
        }
        const inv = 1 / den;
        const o = (j * this.dstW + i) * 4;
        dst.data[o] = cr * inv;
        dst.data[o + 1] = cg * inv;
        dst.data[o + 2] = cb * inv;
        dst.data[o + 3] = ca * inv;
      }
      this.rowsRendered++;
    }
  }

  dirtyRect(srcDirty: Rect): Rect | null {
    const r = intersectRect(srcDirty, fullRect(this.srcW, this.srcH));
    if (!r) return null;
    if (this.filter === 'bilinear') {
      return this.dirtyRectBilinear(r);
    }
    return this.dirtyRectFootprint(r);
  }

  private dirtyRectBilinear(r: Rect): Rect | null {
    const ex0 = r.x - 1;
    const ey0 = r.y - 1;
    const ex1 = r.x + r.w + 1;
    const ey1 = r.y + r.h + 1;
    const corners: Pt[] = [
      apply(this.forward, ex0, ey0),
      apply(this.forward, ex1, ey0),
      apply(this.forward, ex1, ey1),
      apply(this.forward, ex0, ey1),
    ];
    let minx = Infinity;
    let miny = Infinity;
    let maxx = -Infinity;
    let maxy = -Infinity;
    for (const p of corners) {
      minx = Math.min(minx, p[0]);
      miny = Math.min(miny, p[1]);
      maxx = Math.max(maxx, p[0]);
      maxy = Math.max(maxy, p[1]);
    }
    const i0 = Math.max(0, Math.floor(minx - 0.5) - 1);
    const j0 = Math.max(0, Math.floor(miny - 0.5) - 1);
    const i1 = Math.min(this.dstW - 1, Math.ceil(maxx - 0.5) + 1);
    const j1 = Math.min(this.dstH - 1, Math.ceil(maxy - 0.5) + 1);
    let out: Rect | null = null;
    const eps = 1e-9;
    for (let j = j0; j <= j1; j++) {
      for (let i = i0; i <= i1; i++) {
        const [sx, sy] = apply(this.inverse, i + 0.5, j + 0.5);
        if (sx >= ex0 - eps && sx <= ex1 + eps && sy >= ey0 - eps && sy <= ey1 + eps) {
          out = out ? unionRect(out, { x: i, y: j, w: 1, h: 1 }) : { x: i, y: j, w: 1, h: 1 };
        }
      }
    }
    return out;
  }

  private dirtyRectFootprint(r: Rect): Rect | null {
    let poly: Pt[] = [
      apply(this.forward, r.x, r.y),
      apply(this.forward, r.x + r.w, r.y),
      apply(this.forward, r.x + r.w, r.y + r.h),
      apply(this.forward, r.x, r.y + r.h),
    ];
    if (polygonSignedArea(poly) < 0) {
      poly = [poly[0], poly[3], poly[2], poly[1]];
    }
    let minx = Infinity;
    let miny = Infinity;
    let maxx = -Infinity;
    let maxy = -Infinity;
    for (const p of poly) {
      minx = Math.min(minx, p[0]);
      miny = Math.min(miny, p[1]);
      maxx = Math.max(maxx, p[0]);
      maxy = Math.max(maxy, p[1]);
    }
    const i0 = Math.max(0, Math.floor(minx));
    const j0 = Math.max(0, Math.floor(miny));
    const i1 = Math.min(this.dstW - 1, Math.ceil(maxx) - 1);
    const j1 = Math.min(this.dstH - 1, Math.ceil(maxy) - 1);
    let out: Rect | null = null;
    for (let j = j0; j <= j1; j++) {
      for (let i = i0; i <= i1; i++) {
        const sq: Pt[] = [
          [i, j],
          [i + 1, j],
          [i + 1, j + 1],
          [i, j + 1],
        ];
        if (intersectionArea(sq, poly) > 1e-9) {
          out = out ? unionRect(out, { x: i, y: j, w: 1, h: 1 }) : { x: i, y: j, w: 1, h: 1 };
        }
      }
    }
    return out;
  }
}
