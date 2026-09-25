import { RGBAImage } from "./rgba-image.ts";

export type Connectivity = 4 | 8;

export interface PixelUpdate {
  x: number;
  y: number;
  foreground: boolean;
  /** false when the image pixel already matched the tracked state. */
  changed: boolean;
}

/**
 * Maintains connected-component labels for a binary view of an RGBA image.
 *
 * Stable representative ID policy:
 *  - IDs are small integers handed out from a monotonic counter; 0 = background.
 *  - Each component has an anchor pixel (its representative). The anchor never
 *    changes while the component survives.
 *  - Merge (a pixel turns on and touches k >= 1 components): the largest
 *    component keeps its ID (tie: smallest ID); the others are relabeled.
 *  - Split (a pixel turns off and disconnects its component): the
 *    sub-component containing the anchor keeps the ID; the others get fresh
 *    IDs. If the removed pixel was the anchor, the largest sub-component
 *    keeps the ID.
 *  - Components not touched by an edit keep their IDs, guaranteed: updates
 *    only ever relabel pixels of the affected component(s).
 */
export class ComponentLabeler {
  readonly width: number;
  readonly height: number;
  readonly connectivity: Connectivity;

  private foreground: Uint8Array;
  private labelGrid: Int32Array;
  private members = new Map<number, Set<number>>();
  private anchors = new Map<number, number>();
  private nextId = 1;

  constructor(image: RGBAImage, connectivity: Connectivity = 4) {
    if (connectivity !== 4 && connectivity !== 8) {
      throw new RangeError(`connectivity must be 4 or 8, got ${connectivity}`);
    }
    this.width = image.width;
    this.height = image.height;
    this.connectivity = connectivity;
    this.foreground = new Uint8Array(this.width * this.height);
    this.labelGrid = new Int32Array(this.width * this.height);
    this.rebuild(image);
  }

  get componentCount(): number {
    return this.members.size;
  }

  /** Component ID at (x, y); 0 means background. */
  labelAt(x: number, y: number): number {
    return this.labelGrid[this.indexOf(x, y)];
  }

  /** Live view of the label grid (row-major, 0 = background). */
  get labels(): Int32Array {
    return this.labelGrid;
  }

  componentSize(id: number): number {
    return this.members.get(id)?.size ?? 0;
  }

  /** Representative (anchor) pixel of a component, stable while it survives. */
  representativeOf(id: number): { x: number; y: number } | undefined {
    const idx = this.anchors.get(id);
    if (idx === undefined) return undefined;
    return { x: idx % this.width, y: Math.floor(idx / this.width) };
  }

  /**
   * Re-read one pixel from the image and incrementally update only the
   * affected component(s): merges on foreground, splits on background.
   */
  updatePixel(image: RGBAImage, x: number, y: number): PixelUpdate {
    const idx = this.indexOf(x, y);
    const isFg = image.isForeground(x, y);
    const wasFg = this.foreground[idx] === 1;
    if (isFg === wasFg) {
      return { x, y, foreground: isFg, changed: false };
    }
    if (isFg) this.addPixel(idx);
    else this.removePixel(idx);
    return { x, y, foreground: isFg, changed: true };
  }

  /** Full relabel from scratch; IDs are assigned in scan order (1..n). */
  rebuild(image: RGBAImage): void {
    this.foreground.fill(0);
    this.labelGrid.fill(0);
    this.members.clear();
    this.anchors.clear();
    this.nextId = 1;
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        if (image.isForeground(x, y)) {
          this.foreground[y * this.width + x] = 1;
        }
      }
    }
    for (let idx = 0; idx < this.foreground.length; idx++) {
      if (this.foreground[idx] === 1 && this.labelGrid[idx] === 0) {
        this.floodNewComponent(idx);
      }
    }
  }

  private floodNewComponent(start: number): void {
    const id = this.nextId++;
    const set = new Set<number>();
    const stack = [start];
    this.labelGrid[start] = id;
    while (stack.length > 0) {
      const cur = stack.pop()!;
      set.add(cur);
      for (const nb of this.neighborIndices(cur)) {
        if (this.foreground[nb] === 1 && this.labelGrid[nb] === 0) {
          this.labelGrid[nb] = id;
          stack.push(nb);
        }
      }
    }
    this.members.set(id, set);
    this.anchors.set(id, start);
  }

  private addPixel(idx: number): void {
    this.foreground[idx] = 1;
    const neighborIds = new Set<number>();
    for (const nb of this.neighborIndices(idx)) {
      if (this.foreground[nb] === 1) neighborIds.add(this.labelGrid[nb]);
    }
    if (neighborIds.size === 0) {
      const id = this.nextId++;
      this.labelGrid[idx] = id;
      this.members.set(id, new Set([idx]));
      this.anchors.set(id, idx);
      return;
    }
    // Merge: largest component survives (tie: smallest ID).
    let survivor = -1;
    let survivorSize = -1;
    for (const id of neighborIds) {
      const size = this.members.get(id)!.size;
      if (size > survivorSize || (size === survivorSize && id < survivor)) {
        survivor = id;
        survivorSize = size;
      }
    }
    const target = this.members.get(survivor)!;
    for (const id of neighborIds) {
      if (id === survivor) continue;
      for (const member of this.members.get(id)!) {
        this.labelGrid[member] = survivor;
        target.add(member);
      }
      this.members.delete(id);
      this.anchors.delete(id);
    }
    target.add(idx);
    this.labelGrid[idx] = survivor;
  }

  private removePixel(idx: number): void {
    const id = this.labelGrid[idx];
    const set = this.members.get(id)!;
    set.delete(idx);
    this.labelGrid[idx] = 0;
    this.foreground[idx] = 0;
    if (set.size === 0) {
      this.members.delete(id);
      this.anchors.delete(id);
      return;
    }
    // Partition the remaining pixels into connected sub-components.
    const remaining = new Set(set);
    const parts: number[][] = [];
    while (remaining.size > 0) {
      const start = remaining.values().next().value!;
      remaining.delete(start);
      const part = [start];
      const stack = [start];
      while (stack.length > 0) {
        const cur = stack.pop()!;
        for (const nb of this.neighborIndices(cur)) {
          if (remaining.delete(nb)) {
            part.push(nb);
            stack.push(nb);
          }
        }
      }
      parts.push(part);
    }
    if (parts.length === 1) return; // still connected, labels untouched

    const oldAnchor = this.anchors.get(id)!;
    let keepIdx = parts.findIndex((part) => part.includes(oldAnchor));
    if (keepIdx < 0) {
      // The anchor itself was removed: largest part keeps the ID.
      let bestSize = -1;
      for (let i = 0; i < parts.length; i++) {
        if (parts[i].length > bestSize) {
          bestSize = parts[i].length;
          keepIdx = i;
        }
      }
    }
    for (let i = 0; i < parts.length; i++) {
      if (i === keepIdx) {
        this.members.set(id, new Set(parts[i]));
        continue;
      }
      const newId = this.nextId++;
      this.members.set(newId, new Set(parts[i]));
      this.anchors.set(newId, parts[i][0]);
      for (const member of parts[i]) this.labelGrid[member] = newId;
    }
  }

  private indexOf(x: number, y: number): number {
    if (x < 0 || x >= this.width || y < 0 || y >= this.height) {
      throw new RangeError(`pixel out of bounds: (${x}, ${y})`);
    }
    return y * this.width + x;
  }

  private *neighborIndices(idx: number): Generator<number> {
    const x = idx % this.width;
    const y = Math.floor(idx / this.width);
    const w = this.width;
    const h = this.height;
    if (x > 0) yield idx - 1;
    if (x < w - 1) yield idx + 1;
    if (y > 0) yield idx - w;
    if (y < h - 1) yield idx + w;
    if (this.connectivity === 8) {
      if (x > 0 && y > 0) yield idx - w - 1;
      if (x < w - 1 && y > 0) yield idx - w + 1;
      if (x > 0 && y < h - 1) yield idx + w - 1;
      if (x < w - 1 && y < h - 1) yield idx + w + 1;
    }
  }
}
