import type { Rect } from './math.ts';
import { invert, applyToPoint, intersect, isEmpty } from './math.ts';
import { Bitmap } from './bitmap.ts';
import { Layer } from './layer.ts';

/**
 * Renders the layer tree into `target`. When `dirty` is given, only pixels
 * inside that device-space rect are written (incremental repaint); everything
 * else in `target` is left untouched.
 */
export function render(root: Layer, target: Bitmap, dirty?: Rect): void {
  if (dirty) {
    // Incremental repaint: reset the dirty region first, then redraw every
    // layer clipped to it, so the result matches a full render exactly.
    const x0 = Math.max(0, Math.floor(dirty.x));
    const y0 = Math.max(0, Math.floor(dirty.y));
    const x1 = Math.min(target.width, Math.ceil(dirty.x + dirty.width));
    const y1 = Math.min(target.height, Math.ceil(dirty.y + dirty.height));
    for (let y = y0; y < y1; y++) {
      for (let x = x0; x < x1; x++) target.setPixel(x, y, 0, 0, 0, 0);
    }
  }
  const layers: Layer[] = [];
  (function walk(l: Layer) {
    layers.push(l);
    for (const c of l.children) walk(c);
  })(root);

  for (const layer of layers) {
    if (!layer.bitmap) continue;
    renderLayer(layer, target, dirty ?? null);
  }
}

function renderLayer(layer: Layer, target: Bitmap, dirty: Rect | null): void {
  const bounds = layer.contentBoundsDevice(true);
  if (!bounds) return;
  let area = intersect(bounds, { x: 0, y: 0, width: target.width, height: target.height });
  if (area && dirty) area = intersect(area, dirty);
  if (!area || isEmpty(area)) return;

  const world = layer.worldTransform();
  const inv = invert(world);
  const opacity = layer.worldOpacity();
  if (opacity <= 0) return;

  const shadow = layer.effects.find((e) => e.kind === 'shadow');
  const bmp = layer.bitmap!;

  const x0 = Math.max(0, Math.floor(area.x));
  const y0 = Math.max(0, Math.floor(area.y));
  const x1 = Math.min(target.width, Math.ceil(area.x + area.width));
  const y1 = Math.min(target.height, Math.ceil(area.y + area.height));

  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) {
      const local = applyToPoint(inv, x + 0.5, y + 0.5);
      const lx = Math.floor(local.x);
      const ly = Math.floor(local.y);
      const inside = lx >= 0 && ly >= 0 && lx < bmp.width && ly < bmp.height;

      if (inside) {
        const [r, g, b, a] = bmp.getPixel(lx, ly);
        target.blendPixel(x, y, r, g, b, a, layer.blendMode, opacity);
      } else if (shadow && shadow.kind === 'shadow') {
        // Shadow: silhouette of the bitmap shifted by the declared offset,
        // alpha fading linearly to zero across the declared radius.
        const sx = Math.floor(local.x - shadow.offsetX);
        const sy = Math.floor(local.y - shadow.offsetY);
        const dist = distanceOutside(sx, sy, bmp.width, bmp.height);
        if (dist >= 0 && dist <= shadow.radius && shadow.radius > 0) {
          const falloff = 1 - dist / shadow.radius;
          const [r, g, b, a] = shadow.color;
          target.blendPixel(x, y, r, g, b, a * falloff, layer.blendMode, opacity);
        }
      }
    }
  }
}

/** Distance from point to the bitmap rect, or -1 when inside. */
function distanceOutside(px: number, py: number, w: number, h: number): number {
  const dx = Math.max(-px, 0, px - (w - 1));
  const dy = Math.max(-py, 0, py - (h - 1));
  if (dx === 0 && dy === 0) return -1;
  return Math.hypot(dx, dy);
}
