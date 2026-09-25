import type { Point } from './path.ts';
import { createMask } from './raster.ts';
import type { Mask } from './raster.ts';

function isLeft(p: Point, q: Point, x: number, y: number): number {
  return (q.x - p.x) * (y - p.y) - (x - p.x) * (q.y - p.y);
}

export function windingAt(contours: Point[][], x: number, y: number): number {
  let winding = 0;
  for (const contour of contours) {
    for (let i = 0; i < contour.length; i++) {
      const p = contour[i];
      const q = contour[(i + 1) % contour.length];
      if (p.y <= y) {
        if (q.y > y && isLeft(p, q, x, y) > 0) winding += 1;
      } else if (q.y <= y && isLeft(p, q, x, y) < 0) {
        winding -= 1;
      }
    }
  }
  return winding;
}

export function referenceMask(contours: Point[][], width: number, height: number, samples = 32): Mask {
  const mask = createMask(width, height);
  const total = samples * samples;
  for (let py = 0; py < height; py++) {
    for (let px = 0; px < width; px++) {
      let hits = 0;
      for (let sy = 0; sy < samples; sy++) {
        const y = py + (sy + 0.5) / samples;
        for (let sx = 0; sx < samples; sx++) {
          const x = px + (sx + 0.5) / samples;
          if (windingAt(contours, x, y) !== 0) hits += 1;
        }
      }
      mask.data[py * width + px] = hits / total;
    }
  }
  return mask;
}
