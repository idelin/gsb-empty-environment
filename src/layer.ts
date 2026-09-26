import type { Matrix, Rect } from './math.ts';
import { identity, multiply, transformRect, intersect, expand, offset, maxScale } from './math.ts';
import { Bitmap, BlendMode } from './bitmap.ts';

/** Drop-shadow style effect; `radius` is the declared blur/spread radius in local units. */
export interface ShadowEffect {
  kind: 'shadow';
  radius: number;
  offsetX: number;
  offsetY: number;
  color: [number, number, number, number];
}
export type Effect = ShadowEffect;

export class Layer {
  readonly id: string;
  bitmap: Bitmap | null = null;
  transform: Matrix = identity();
  opacity = 1;
  blendMode: BlendMode = BlendMode.SourceOver;
  /** Clip rectangle in this layer's local coordinate space; applies to the whole subtree. */
  clip: Rect | null = null;
  effects: Effect[] = [];
  readonly children: Layer[] = [];
  parent: Layer | null = null;

  constructor(id: string, opts: Partial<{
    bitmap: Bitmap; transform: Matrix; opacity: number; blendMode: BlendMode;
    clip: Rect | null; effects: Effect[];
  }> = {}) {
    this.id = id;
    if (opts.bitmap !== undefined) this.bitmap = opts.bitmap;
    if (opts.transform !== undefined) this.transform = opts.transform;
    if (opts.opacity !== undefined) this.opacity = opts.opacity;
    if (opts.blendMode !== undefined) this.blendMode = opts.blendMode;
    if (opts.clip !== undefined) this.clip = opts.clip;
    if (opts.effects !== undefined) this.effects = opts.effects;
  }

  addChild(child: Layer): Layer {
    child.parent = this;
    this.children.push(child);
    return child;
  }

  /** Local bounds of the layer's own content (no children, no effects). */
  localBounds(): Rect | null {
    if (!this.bitmap) return null;
    return { x: 0, y: 0, width: this.bitmap.width, height: this.bitmap.height };
  }

  /** Extra device-space expansion caused by declared effects (shadow radius + offset). */
  effectExpansionDevice(): { radius: number; dx: number; dy: number } {
    let radius = 0;
    let dx = 0;
    let dy = 0;
    for (const e of this.effects) {
      if (e.kind === 'shadow') {
        radius = Math.max(radius, e.radius);
        dx = Math.abs(e.offsetX) > Math.abs(dx) ? e.offsetX : dx;
        dy = Math.abs(e.offsetY) > Math.abs(dy) ? e.offsetY : dy;
      }
    }
    const s = maxScale(this.worldTransform());
    return { radius: radius * s, dx: dx * s, dy: dy * s };
  }

  worldTransform(): Matrix {
    const chain: Layer[] = [];
    for (let l: Layer | null = this; l; l = l.parent) chain.push(l);
    let m = identity();
    for (let i = chain.length - 1; i >= 0; i--) m = multiply(m, chain[i].transform);
    return m;
  }

  /** Accumulated opacity from the root down to (and including) this layer. */
  worldOpacity(): number {
    let o = 1;
    for (let l: Layer | null = this; l; l = l.parent) o *= l.opacity;
    return o;
  }

  /**
   * Effective clip in device (root) space: intersection of every ancestor clip
   * (and this layer's own clip), each mapped through that layer's world transform.
   * Conservative: rotated clips become their axis-aligned bounds.
   */
  effectiveClipDevice(): Rect | null {
    let clip: Rect | null = null;
    for (let l: Layer | null = this; l; l = l.parent) {
      if (!l.clip) continue;
      const deviceClip = transformRect(l.worldTransform(), l.clip);
      clip = clip === null ? deviceClip : intersect(clip, deviceClip);
      if (clip === null) return null;
    }
    return clip;
  }

  /** Device-space bounds of own content including effect expansion. Null if no bitmap. */
  contentBoundsDevice(includeEffects = true): Rect | null {
    const local = this.localBounds();
    if (!local) return null;
    let device = transformRect(this.worldTransform(), local);
    if (includeEffects) {
      const { radius, dx, dy } = this.effectExpansionDevice();
      if (radius > 0 || dx !== 0 || dy !== 0) {
        const grown = expand(device, radius, radius);
        const shifted = offset(grown, dx, dy);
        device = {
          x: Math.min(grown.x, shifted.x),
          y: Math.min(grown.y, shifted.y),
          width: Math.max(grown.x + grown.width, shifted.x + shifted.width) - Math.min(grown.x, shifted.x),
          height: Math.max(grown.y + grown.height, shifted.y + shifted.height) - Math.min(grown.y, shifted.y),
        };
      }
    }
    const clip = this.effectiveClipDevice();
    return clip === null ? device : intersect(device, clip);
  }
}
