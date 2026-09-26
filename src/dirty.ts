import type { Matrix, Rect } from './math.ts';
import { transformRect, intersect, union, isEmpty, roundOut } from './math.ts';
import type { Effect } from './layer.ts';
import { Layer } from './layer.ts';

/** Why a region needs repainting. */
export const RepaintReason = {
  LocalEdit: 'local-edit',
  TransformChanged: 'transform-changed',
  OpacityChanged: 'opacity-changed',
  ClipChanged: 'clip-changed',
  EffectChanged: 'effect-changed',
} as const;
export type RepaintReason = (typeof RepaintReason)[keyof typeof RepaintReason];

export interface DirtyEntry {
  /** Dirty rect in device (root) pixels, rounded outward to integers. */
  rect: Rect;
  reason: RepaintReason;
  /** Layer whose property changed. */
  layerId: string;
  /** Propagation path from the changed layer up to the root (inclusive). */
  propagationPath: string[];
  detail: string;
}

function pathToRoot(layer: Layer): string[] {
  const path: string[] = [];
  for (let l: Layer | null = layer; l; l = l.parent) path.push(l.id);
  return path;
}

function fmt(r: Rect): string {
  return `(${r.x},${r.y} ${r.width}x${r.height})`;
}

/**
 * Computes minimal conservative dirty regions for a layer tree.
 * All dirty rects live in device (root) pixel coordinates.
 *
 * Mutation helpers (setTransform/setOpacity/setClip/setEffects) snapshot the
 * layer's bounds before applying the change, so the dirty region always covers
 * the union of the old and new footprints.
 */
export class DirtyTracker {
  readonly root: Layer;
  private entries: DirtyEntry[] = [];

  constructor(root: Layer) {
    this.root = root;
  }

  getEntries(): readonly DirtyEntry[] {
    return this.entries;
  }

  /** Merged dirty region (union of all entries). Null when nothing is dirty. */
  dirtyRegion(): Rect | null {
    let out: Rect | null = null;
    for (const e of this.entries) out = out === null ? e.rect : union(out, e.rect);
    return out;
  }

  clear(): void {
    this.entries = [];
  }

  /**
   * Records a device-space rect. Bounds passed here are already clip-aware
   * (contentBoundsDevice intersects the effective clip), except local edits,
   * which are clipped by markLocalEdit itself.
   */
  private push(layer: Layer, rect: Rect | null, reason: RepaintReason, detail: string): void {
    if (rect === null || isEmpty(rect)) return;
    this.entries.push({
      rect: roundOut(rect),
      reason,
      layerId: layer.id,
      propagationPath: pathToRoot(layer),
      detail,
    });
  }

  /**
   * A local edit inside `layer`'s content, given in the layer's local coordinates.
   * Mapped to device space, expanded by the declared effect radius (shadow of the
   * edited pixels changes too), then intersected with all ancestor clips.
   * Edits fully outside the clip produce no dirty region.
   */
  markLocalEdit(layer: Layer, localRect: Rect): void {
    let device = transformRect(layer.worldTransform(), localRect);
    const { radius, dx, dy } = layer.effectExpansionDevice();
    if (radius > 0 || dx !== 0 || dy !== 0) {
      const grow = radius + Math.max(Math.abs(dx), Math.abs(dy));
      device = {
        x: device.x - grow, y: device.y - grow,
        width: device.width + 2 * grow, height: device.height + 2 * grow,
      };
    }
    const clip = layer.effectiveClipDevice();
    const clipped = clip === null ? device : intersect(device, clip);
    if (clipped === null || isEmpty(clipped)) return; // fully clipped away: no repaint
    this.push(layer, clipped, RepaintReason.LocalEdit,
      `local edit ${fmt(localRect)} in layer '${layer.id}'`);
  }

  /** Move/scale/rotate: dirty region covers old position + new position. */
  setTransform(layer: Layer, transform: Matrix): void {
    const oldBounds = layer.contentBoundsDevice(true);
    layer.transform = transform;
    const newBounds = layer.contentBoundsDevice(true);
    const both = oldBounds && newBounds ? union(oldBounds, newBounds) : oldBounds ?? newBounds;
    this.push(layer, both, RepaintReason.TransformChanged,
      `transform of '${layer.id}' changed; old ${oldBounds ? fmt(oldBounds) : 'none'} + new ${newBounds ? fmt(newBounds) : 'none'}`);
  }

  /** Opacity changes propagate up the tree: the whole composited footprint is dirty. */
  setOpacity(layer: Layer, opacity: number): void {
    layer.opacity = opacity;
    this.push(layer, layer.contentBoundsDevice(true), RepaintReason.OpacityChanged,
      `opacity of '${layer.id}' -> ${opacity}; propagated ${pathToRoot(layer).join(' -> ')}`);
  }

  /** Clip changed: dirty region covers old + new visible bounds. */
  setClip(layer: Layer, clip: Rect | null): void {
    const oldBounds = layer.contentBoundsDevice(true);
    layer.clip = clip;
    const newBounds = layer.contentBoundsDevice(true);
    const both = oldBounds && newBounds ? union(oldBounds, newBounds) : oldBounds ?? newBounds;
    this.push(layer, both, RepaintReason.ClipChanged,
      `clip of '${layer.id}' changed; old+new clipped bounds repainted`);
  }

  /** Effect (e.g. shadow radius) changed: dirty region covers old + new expanded bounds. */
  setEffects(layer: Layer, effects: Effect[]): void {
    const oldBounds = layer.contentBoundsDevice(true);
    layer.effects = effects;
    const newBounds = layer.contentBoundsDevice(true);
    const both = oldBounds && newBounds ? union(oldBounds, newBounds) : oldBounds ?? newBounds;
    this.push(layer, both, RepaintReason.EffectChanged,
      `effects of '${layer.id}' changed; old+new effect bounds repainted`);
  }
}
