import {
  composeTransform,
  IDENTITY,
  inflate,
  intersect,
  isEmpty,
  rect,
  transformRect,
  translate,
  type Rect,
  type Transform,
} from './geometry.ts';
import { Layer, Scene, type BlendMode } from './layer.ts';

function blendChannel(mode: BlendMode, s: number, d: number): number {
  switch (mode) {
    case 'multiply':
      return s * d;
    case 'additive':
      return Math.min(1, s + d);
    default:
      return s;
  }
}

/** Source-over compositing with a per-pixel blend mode, in straight alpha. */
function compositePixel(
  buf: Uint8ClampedArray,
  bufWidth: number,
  x: number,
  y: number,
  r: number,
  g: number,
  b: number,
  a: number,
  mode: BlendMode,
  opacity: number,
): void {
  const idx = (y * bufWidth + x) * 4;
  const sa = (a / 255) * opacity;
  const da = buf[idx + 3] / 255;
  const outA = sa + da * (1 - sa);
  if (outA <= 0) {
    buf[idx] = buf[idx + 1] = buf[idx + 2] = buf[idx + 3] = 0;
    return;
  }
  const src = [r / 255, g / 255, b / 255];
  for (let c = 0; c < 3; c++) {
    const dc = buf[idx + c] / 255;
    const blended = blendChannel(mode, src[c], dc);
    const out = (blended * sa * da + src[c] * sa * (1 - da) + dc * da * (1 - sa)) / outA;
    buf[idx + c] = Math.round(out * 255);
  }
  buf[idx + 3] = Math.round(outA * 255);
}

function sampleAlpha(layer: Layer, world: Transform, px: number, py: number): number {
  const sx = Math.floor((px + 0.5 - world.tx) / world.sx);
  const sy = Math.floor((py + 0.5 - world.ty) / world.sy);
  if (sx < 0 || sy < 0 || sx >= layer.width || sy >= layer.height) return 0;
  return layer.pixels[(sy * layer.width + sx) * 4 + 3];
}

function drawShadow(
  scene: Scene,
  buf: Uint8ClampedArray,
  layer: Layer,
  world: Transform,
  bounds: Rect,
  clip: Rect,
): void {
  const shadow = layer.shadow;
  if (!shadow) return;
  const extent = intersect(
    inflate(translate(bounds, shadow.offsetX, shadow.offsetY), shadow.radius),
    clip,
  );
  const radius = shadow.radius;
  for (let y = extent.y; y < extent.y + extent.h; y++) {
    for (let x = extent.x; x < extent.x + extent.w; x++) {
      const cx = x - shadow.offsetX;
      const cy = y - shadow.offsetY;
      let coverage = 0;
      for (let dy = -radius; dy <= radius; dy++) {
        for (let dx = -radius; dx <= radius; dx++) {
          const alpha = sampleAlpha(layer, world, cx + dx, cy + dy);
          if (alpha > coverage) coverage = alpha;
        }
      }
      if (coverage === 0) continue;
      const a = Math.round((coverage * shadow.color[3]) / 255);
      compositePixel(buf, scene.width, x, y, shadow.color[0], shadow.color[1], shadow.color[2], a, 'normal', layer.opacity);
    }
  }
}

function drawLayer(
  scene: Scene,
  buf: Uint8ClampedArray,
  layer: Layer,
  parentTransform: Transform,
  ancestorClip: Rect,
  limit: Rect,
): void {
  const world = composeTransform(parentTransform, layer.transform);
  let clip = ancestorClip;
  if (layer.clip) clip = intersect(clip, transformRect(layer.clip, world));
  clip = intersect(clip, limit);
  const bounds = transformRect(rect(0, 0, layer.width, layer.height), world);
  if (layer.shadow && !isEmpty(clip)) drawShadow(scene, buf, layer, world, bounds, clip);
  const draw = intersect(bounds, clip);
  for (let y = draw.y; y < draw.y + draw.h; y++) {
    for (let x = draw.x; x < draw.x + draw.w; x++) {
      const sx = Math.floor((x + 0.5 - world.tx) / world.sx);
      const sy = Math.floor((y + 0.5 - world.ty) / world.sy);
      if (sx < 0 || sy < 0 || sx >= layer.width || sy >= layer.height) continue;
      const i = (sy * layer.width + sx) * 4;
      compositePixel(
        buf,
        scene.width,
        x,
        y,
        layer.pixels[i],
        layer.pixels[i + 1],
        layer.pixels[i + 2],
        layer.pixels[i + 3],
        layer.blendMode,
        layer.opacity,
      );
    }
  }
  for (const child of layer.children) drawLayer(scene, buf, child, world, clip, limit);
}

/**
 * Renders the scene into `target` (allocated when omitted). When `region` is
 * given, only pixels inside it are cleared and recomputed — pixels outside
 * are left untouched, which is exactly what a dirty-region repaint does.
 */
export function render(
  scene: Scene,
  target?: Uint8ClampedArray,
  region?: Rect,
): Uint8ClampedArray {
  const buf = target ?? new Uint8ClampedArray(scene.width * scene.height * 4);
  const canvas = rect(0, 0, scene.width, scene.height);
  const limit = region ? intersect(region, canvas) : canvas;
  for (let y = limit.y; y < limit.y + limit.h; y++) {
    for (let x = limit.x; x < limit.x + limit.w; x++) {
      const i = (y * scene.width + x) * 4;
      buf[i] = buf[i + 1] = buf[i + 2] = buf[i + 3] = 0;
    }
  }
  for (const root of scene.roots) drawLayer(scene, buf, root, IDENTITY, canvas, limit);
  return buf;
}
