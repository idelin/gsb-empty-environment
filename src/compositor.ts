import {
  composeTransform,
  IDENTITY,
  inflate,
  intersect,
  isEmpty,
  rect,
  transformRect,
  translate,
  union,
  type Rect,
  type Transform,
} from './geometry.ts';
import { Layer, Scene } from './layer.ts';

export interface DirtyRegion {
  rect: Rect;
  reasons: string[];
}

/**
 * Tracks a layer tree and derives the minimal conservative repaint region
 * (in root/canvas device pixels) for every local edit, together with the
 * human-readable reasons that forced the repaint.
 */
export class Compositor {
  readonly scene: Scene;
  private pending: DirtyRegion[] = [];

  constructor(scene: Scene) {
    this.scene = scene;
  }

  private canvasRect(): Rect {
    return rect(0, 0, this.scene.width, this.scene.height);
  }

  worldTransform(layer: Layer): Transform {
    const chain: Layer[] = [];
    let node: Layer | null = layer;
    while (node) {
      chain.push(node);
      node = node.parent;
    }
    let t = IDENTITY;
    for (let i = chain.length - 1; i >= 0; i--) t = composeTransform(t, chain[i].transform);
    return t;
  }

  /** Intersection of every clip on the ancestor chain (incl. the layer itself), in root space. */
  effectiveClip(layer: Layer): Rect {
    const chain: Layer[] = [];
    let node: Layer | null = layer;
    while (node) {
      chain.push(node);
      node = node.parent;
    }
    let clip = this.canvasRect();
    let t = IDENTITY;
    for (let i = chain.length - 1; i >= 0; i--) {
      const n = chain[i];
      t = composeTransform(t, n.transform);
      if (n.clip) clip = intersect(clip, transformRect(n.clip, t));
    }
    return clip;
  }

  /** Full paint footprint of a layer in root space: clipped bounds plus shadow extent. */
  paintBounds(layer: Layer): Rect {
    const world = this.worldTransform(layer);
    const clip = this.effectiveClip(layer);
    const bounds = transformRect(rect(0, 0, layer.width, layer.height), world);
    let paint = intersect(bounds, clip);
    if (layer.shadow && !isEmpty(paint)) {
      const s = layer.shadow;
      const shadowExtent = intersect(
        inflate(translate(paint, s.offsetX, s.offsetY), s.radius),
        clip,
      );
      paint = union(paint, shadowExtent);
    }
    return intersect(paint, this.canvasRect());
  }

  private subtreeBounds(layer: Layer): Map<string, Rect> {
    const map = new Map<string, Rect>();
    for (const node of layer.walk()) map.set(node.id, this.paintBounds(node));
    return map;
  }

  private record(region: Rect, reasons: string[]): DirtyRegion {
    const dirty: DirtyRegion = { rect: region, reasons };
    if (!isEmpty(region)) this.pending.push(dirty);
    return dirty;
  }

  /** Local pixel edit inside `localRect` (layer content space). */
  editContent(layer: Layer, localRect: Rect): DirtyRegion {
    const world = this.worldTransform(layer);
    const clip = this.effectiveClip(layer);
    let region = intersect(transformRect(localRect, world), clip);
    if (isEmpty(region)) {
      return { rect: region, reasons: [`outside-clip(${layer.id}):no-repaint`] };
    }
    const reasons = [`content-changed(${layer.id})`];
    if (layer.shadow) {
      const s = layer.shadow;
      region = union(region, intersect(inflate(translate(region, s.offsetX, s.offsetY), s.radius), clip));
      reasons.push(`shadow-expanded(radius=${s.radius})`);
    }
    return this.record(intersect(region, this.canvasRect()), reasons);
  }

  /** Transform change: dirties old + new footprints of the whole subtree. */
  setTransform(layer: Layer, transform: Transform): DirtyRegion {
    const before = this.subtreeBounds(layer);
    layer.transform = { ...transform };
    const after = this.subtreeBounds(layer);
    let merged = rect(0, 0, 0, 0);
    const reasons: string[] = [];
    for (const [id, oldRect] of before) {
      const footprint = union(oldRect, after.get(id)!);
      if (!isEmpty(footprint)) merged = union(merged, footprint);
      reasons.push(
        id === layer.id ? `moved(${id}):old+new-bounds` : `parent-transform-changed(${id})`,
      );
    }
    return this.record(merged, reasons);
  }

  /** Opacity change propagates to the whole subtree footprint. */
  setOpacity(layer: Layer, opacity: number): DirtyRegion {
    layer.opacity = opacity;
    let merged = rect(0, 0, 0, 0);
    const reasons: string[] = [];
    for (const node of layer.walk()) {
      const footprint = this.paintBounds(node);
      if (!isEmpty(footprint)) merged = union(merged, footprint);
      reasons.push(
        node === layer ? `opacity-changed(${node.id})` : `ancestor-opacity-changed(${node.id})`,
      );
    }
    return this.record(merged, reasons);
  }

  /** Clip change: repaints the union of the old and new clipped footprints. */
  setClip(layer: Layer, clip: Rect | null): DirtyRegion {
    const before = this.paintBounds(layer);
    layer.clip = clip ? { ...clip } : null;
    const after = this.paintBounds(layer);
    return this.record(union(before, after), [`clip-changed(${layer.id})`]);
  }

  initialDirty(): DirtyRegion {
    return this.record(this.canvasRect(), ['initial-render']);
  }

  /** Merges and clears all accumulated dirty regions. */
  takeDirty(): DirtyRegion {
    let merged = rect(0, 0, 0, 0);
    const reasons = new Set<string>();
    for (const entry of this.pending) {
      merged = union(merged, entry.rect);
      for (const reason of entry.reasons) reasons.add(reason);
    }
    this.pending = [];
    return { rect: merged, reasons: [...reasons] };
  }
}
