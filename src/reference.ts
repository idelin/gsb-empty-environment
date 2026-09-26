import { RGBAImage } from "./image.ts";
import { projectFace } from "./project.ts";
import { blendOver, depthAt, prepareTri, shade, triangleCover } from "./raster.ts";
import type { Face, Viewport } from "./types.ts";

const EMPTY = 0xffffffff;

/**
 * Single-threaded per-pixel reference implementation. Structurally
 * independent from the tiled renderer: for every pixel it walks every face
 * directly. Used as the ground truth for the tiled/parallel renderer.
 */
export function renderReference(
  faces: Iterable<Face>,
  width: number,
  height: number,
  viewport: Viewport,
): RGBAImage {
  const img = new RGBAImage(width, height);
  const projected = [];
  for (const f of faces) {
    const p = projectFace(f, viewport);
    if (p) projected.push(p);
  }
  const opaque = projected.filter((p) => !p.transparent);
  const transparent = projected.filter((p) => p.transparent).sort((a, b) => a.id - b.id);
  for (let py = viewport.y; py < viewport.y + viewport.height; py++) {
    for (let px = viewport.x; px < viewport.x + viewport.width; px++) {
      let bestDepth = EMPTY;
      let bestId = EMPTY;
      for (const f of opaque) {
        for (const tri of f.tris) {
          const t = prepareTri(tri);
          if (!t) continue;
          const cov = triangleCover(t, px, py);
          if (!cov) continue;
          const d = depthAt(t, cov);
          if (d < bestDepth || (d === bestDepth && f.id < bestId)) {
            const c = shade(t, cov);
            if (!c) continue;
            bestDepth = d;
            bestId = f.id;
            img.set(px, py, c[0], c[1], c[2], c[3]);
          }
        }
      }
      for (const f of transparent) {
        for (const tri of f.tris) {
          const t = prepareTri(tri);
          if (!t) continue;
          const cov = triangleCover(t, px, py);
          if (!cov) continue;
          const d = depthAt(t, cov);
          if (d <= bestDepth) {
            const c = shade(t, cov);
            if (!c) continue;
            blendOver(img, px, py, c);
          }
        }
      }
    }
  }
  return img;
}
