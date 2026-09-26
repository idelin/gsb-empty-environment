import { FP_ONE } from "./fixed.ts";
import type { RGBAImage } from "./image.ts";
import type { ScreenVertex, Tri } from "./types.ts";

export interface Scissor {
  x0: number;
  y0: number;
  x1: number; // exclusive
  y1: number; // exclusive
}

/** Exact 2D edge function on 16.16 fixed-point coords (BigInt => no rounding, no overflow). */
export function edge(ax: number, ay: number, bx: number, by: number, px: number, py: number): bigint {
  return BigInt(bx - ax) * BigInt(py - ay) - BigInt(by - ay) * BigInt(px - ax);
}

/**
 * Top-left rule (y-down screen, triangle wound so the interior has positive
 * edge functions): an edge owns its on-edge pixels iff it is a "left" edge
 * (goes up, dy < 0) or a "top" edge (horizontal, goes right, dx > 0).
 */
export function isTopLeft(dx: number, dy: number): boolean {
  return dy < 0 || (dy === 0 && dx > 0);
}

/** Normalize winding so the signed area is positive. Degenerate (zero-area) => null. */
export function prepareTri(tri: Tri): Tri | null {
  const [a, b, c] = tri;
  const area = edge(a.fx, a.fy, b.fx, b.fy, c.fx, c.fy);
  if (area === 0n) return null;
  return area > 0n ? tri : [a, c, b];
}

export interface Coverage {
  es: [bigint, bigint, bigint];
  area: bigint;
}

/** Exact coverage test of pixel center (px+0.5, py+0.5) with the top-left rule. */
export function triangleCover(t: Tri, px: number, py: number): Coverage | null {
  const sx = px * FP_ONE + FP_ONE / 2;
  const sy = py * FP_ONE + FP_ONE / 2;
  const es: [bigint, bigint, bigint] = [
    edge(t[1].fx, t[1].fy, t[2].fx, t[2].fy, sx, sy),
    edge(t[2].fx, t[2].fy, t[0].fx, t[0].fy, sx, sy),
    edge(t[0].fx, t[0].fy, t[1].fx, t[1].fy, sx, sy),
  ];
  const dirs: [number, number][] = [
    [t[2].fx - t[1].fx, t[2].fy - t[1].fy],
    [t[0].fx - t[2].fx, t[0].fy - t[2].fy],
    [t[1].fx - t[0].fx, t[1].fy - t[0].fy],
  ];
  for (let i = 0; i < 3; i++) {
    if (es[i] < 0n) return null;
    if (es[i] === 0n && !isTopLeft(dirs[i][0], dirs[i][1])) return null;
  }
  return { es, area: es[0] + es[1] + es[2] };
}

/** Screen-linear 24-bit depth from exact barycentrics (deterministic integer math). */
export function depthAt(t: Tri, cov: Coverage): number {
  const d =
    (cov.es[0] * BigInt(t[0].depth) + cov.es[1] * BigInt(t[1].depth) + cov.es[2] * BigInt(t[2].depth)) /
    cov.area;
  return Number(d);
}

/**
 * Perspective-correct attribute interpolation: sum(lambda_i * a_i / w_i) /
 * sum(lambda_i / w_i). Returns 8-bit channels, or null if not representable.
 */
export function shade(t: Tri, cov: Coverage): number[] | null {
  const area = Number(cov.area);
  const l0 = Number(cov.es[0]) / area;
  const l1 = Number(cov.es[1]) / area;
  const l2 = Number(cov.es[2]) / area;
  const ks = l0 * t[0].invW + l1 * t[1].invW + l2 * t[2].invW;
  const out = new Array<number>(t[0].attrsOverW.length);
  for (let i = 0; i < out.length; i++) {
    const c =
      (l0 * t[0].attrsOverW[i] + l1 * t[1].attrsOverW[i] + l2 * t[2].attrsOverW[i]) / ks;
    if (!Number.isFinite(c)) return null;
    out[i] = c <= 0 ? 0 : c >= 255 ? 255 : Math.round(c);
  }
  return out;
}

/** Deterministic "over" blend: out = (src*a + dst*(255-a) + 127) / 255. */
export function blendOver(img: RGBAImage, px: number, py: number, src: number[]): void {
  const idx = (py * img.width + px) * 4;
  const a = src[3];
  const inv = 255 - a;
  for (let ch = 0; ch < 4; ch++) {
    img.data[idx + ch] = ((src[ch] * a + img.data[idx + ch] * inv + 127) / 255) | 0;
  }
}

function triBBox(t: Tri): [number, number, number, number] {
  const minFx = Math.min(t[0].fx, t[1].fx, t[2].fx);
  const minFy = Math.min(t[0].fy, t[1].fy, t[2].fy);
  const maxFx = Math.max(t[0].fx, t[1].fx, t[2].fx);
  const maxFy = Math.max(t[0].fy, t[1].fy, t[2].fy);
  return [
    Math.floor(minFx / FP_ONE),
    Math.floor(minFy / FP_ONE),
    Math.floor(maxFx / FP_ONE) + 1,
    Math.floor(maxFy / FP_ONE) + 1,
  ];
}

/**
 * Opaque pass. Depth test: LESS wins; on EQUAL depth the smaller face id wins
 * (stable, order-independent tie-break). Depth and winning id are written.
 */
export function rasterizeOpaque(
  tri: Tri,
  faceId: number,
  img: RGBAImage,
  depth: Uint32Array,
  ids: Uint32Array,
  sc: Scissor,
): void {
  const t = prepareTri(tri);
  if (!t) return;
  const [bx0, by0, bx1, by1] = triBBox(t);
  const x0 = Math.max(sc.x0, bx0);
  const y0 = Math.max(sc.y0, by0);
  const x1 = Math.min(sc.x1, bx1);
  const y1 = Math.min(sc.y1, by1);
  for (let py = y0; py < y1; py++) {
    for (let px = x0; px < x1; px++) {
      const cov = triangleCover(t, px, py);
      if (!cov) continue;
      const d = depthAt(t, cov);
      const idx = py * img.width + px;
      if (d < depth[idx] || (d === depth[idx] && faceId < ids[idx])) {
        const c = shade(t, cov);
        if (!c) continue;
        depth[idx] = d;
        ids[idx] = faceId;
        img.set(px, py, c[0], c[1], c[2], c[3]);
      }
    }
  }
}

/**
 * Transparent pass (runs after opaque, faces sorted by ascending face id).
 * Rule: a transparent fragment passes iff its depth is LESS-or-EQUAL to the
 * opaque depth at the pixel; it never writes depth and never tests against
 * other transparent faces — transparent layers compose strictly in face-id
 * order, so the result is independent of execution order.
 */
export function rasterizeTransparent(
  tri: Tri,
  faceId: number,
  img: RGBAImage,
  depth: Uint32Array,
  sc: Scissor,
): void {
  const t = prepareTri(tri);
  if (!t) return;
  const [bx0, by0, bx1, by1] = triBBox(t);
  const x0 = Math.max(sc.x0, bx0);
  const y0 = Math.max(sc.y0, by0);
  const x1 = Math.min(sc.x1, bx1);
  const y1 = Math.min(sc.y1, by1);
  for (let py = y0; py < y1; py++) {
    for (let px = x0; px < x1; px++) {
      const cov = triangleCover(t, px, py);
      if (!cov) continue;
      const d = depthAt(t, cov);
      const idx = py * img.width + px;
      if (d <= depth[idx]) {
        const c = shade(t, cov);
        if (!c) continue;
        blendOver(img, px, py, c);
      }
    }
  }
}
