import { toFixed, FP_ONE } from "./fixed.ts";
import { clipTriangle } from "./clip.ts";
import type { Face, ProjectedFace, ScreenVertex, Tri, Viewport } from "./types.ts";

export const DEPTH_MAX = (1 << 24) - 1; // 24-bit depth buffer

/**
 * Clip a face to the view frustum (near/far) and project to the viewport.
 * Screen coordinates become saturated 16.16 fixed point; depth is quantized
 * to 24 bit. Returns null when nothing survives clipping (or coords are NaN).
 */
export function projectFace(face: Face, vp: Viewport): ProjectedFace | null {
  const tris: Tri[] = [];
  let minFx = Infinity;
  let minFy = Infinity;
  let maxFx = -Infinity;
  let maxFy = -Infinity;
  for (const tri of clipTriangle(face.verts)) {
    const sv: ScreenVertex[] = [];
    let ok = true;
    for (const v of tri) {
      if (!(v.w > 0)) {
        ok = false;
        break;
      }
      const sx = vp.x + (v.x / v.w + 1) * 0.5 * vp.width;
      const sy = vp.y + (1 - (v.y / v.w + 1) * 0.5) * vp.height;
      const fx = toFixed(sx);
      const fy = toFixed(sy);
      if (Number.isNaN(fx) || Number.isNaN(fy)) {
        ok = false;
        break;
      }
      const ndz = v.z / v.w;
      const depth = Math.round(Math.min(1, Math.max(0, (ndz + 1) * 0.5)) * DEPTH_MAX);
      sv.push({ fx, fy, depth, invW: 1 / v.w, attrsOverW: v.attrs.map((a) => a / v.w) });
      minFx = Math.min(minFx, fx);
      minFy = Math.min(minFy, fy);
      maxFx = Math.max(maxFx, fx);
      maxFy = Math.max(maxFy, fy);
    }
    if (ok && sv.length === 3) tris.push([sv[0], sv[1], sv[2]]);
  }
  if (tris.length === 0) return null;
  const minX = Math.max(Math.floor(minFx / FP_ONE), vp.x);
  const minY = Math.max(Math.floor(minFy / FP_ONE), vp.y);
  const maxX = Math.min(Math.floor(maxFx / FP_ONE) + 1, vp.x + vp.width);
  const maxY = Math.min(Math.floor(maxFy / FP_ONE) + 1, vp.y + vp.height);
  const transparent = face.verts.some((v) => (v.attrs[3] ?? 255) < 255);
  return { id: face.id, transparent, tris, minX, minY, maxX, maxY };
}
