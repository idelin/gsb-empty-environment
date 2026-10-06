import { IDENTITY, isSingular, multiply, type Mat2D } from "./matrix.ts";
import {
  bboxContains,
  clipBBox,
  intersectBBox,
  rectBBox,
  transformBBox,
  type BBox,
} from "./geometry.ts";
import type { SceneNode } from "./node.ts";
import type { Scene } from "./scene.ts";
import { exactHit, zOrderRanks } from "./hit.ts";
import { liveView, type TreeView } from "./view.ts";

interface CacheEntry {
  key: string;
  bbox: BBox | null;
}

/**
 * Spatial index over world-space bounding boxes.
 *
 * The index only narrows down candidates; real hit testing still runs on
 * local geometry plus ancestor clipping (`exactHit`). A node's effective
 * world bbox is its transformed local rect intersected with the world
 * bboxes of every rect/circle clip on its ancestor path.
 *
 * Cache entries are keyed by the node's ancestor version chain, so
 * updating an ancestor transform invalidates the cached bboxes of the
 * whole subtree. Entries computed under older versions stay in the cache
 * and keep serving snapshot views, whose version chains are unchanged.
 */
export class SpatialIndex {
  private readonly scene: Scene;
  private readonly cache = new Map<number, CacheEntry>();

  constructor(scene: Scene) {
    this.scene = scene;
  }

  /** Version-chain key identifying the node's cached world-space data. */
  cacheKeyOf(node: SceneNode, view: TreeView = liveView()): string | null {
    const chain: SceneNode[] = [];
    for (let n: SceneNode | null = node; n; n = view.parentOf(n)) chain.push(n);
    const parts: string[] = [];
    for (let i = chain.length - 1; i >= 0; i--) {
      if (view.matrixOf(chain[i]) === undefined) return null;
      parts.push(`${chain[i].id}:${view.versionOf(chain[i])}`);
    }
    return parts.join("/");
  }

  /**
   * Effective world bbox: transformed local rect intersected with the
   * transformed bboxes of all ancestor clip shapes. `null` when the node
   * can never be hit (singular chain, or clips cut everything away).
   */
  private computeBBox(node: SceneNode, view: TreeView): BBox | null {
    const chain: SceneNode[] = [];
    for (let n: SceneNode | null = node; n; n = view.parentOf(n)) chain.push(n);
    chain.reverse();

    let world: Mat2D = IDENTITY;
    const worlds: Mat2D[] = [];
    for (const current of chain) {
      const local = view.matrixOf(current);
      if (local === undefined) return null;
      world = multiply(world, local);
      if (isSingular(world)) return null;
      worlds.push(world);
    }

    let bbox = transformBBox(rectBBox(node.localRect), world);
    for (let i = 0; i < chain.length - 1; i++) {
      const clip = chain[i].clip;
      if (!clip) continue;
      const clipWorldBBox = transformBBox(clipBBox(clip), worlds[i]);
      const intersection = intersectBBox(bbox, clipWorldBBox);
      if (intersection === null) return null;
      bbox = intersection;
    }
    return bbox;
  }

  private bboxOf(node: SceneNode, view: TreeView): BBox | null {
    const key = this.cacheKeyOf(node, view);
    if (key === null) return null;
    let entry = this.cache.get(node.id);
    if (!entry || entry.key !== key) {
      entry = { key, bbox: this.computeBBox(node, view) };
      this.cache.set(node.id, entry);
    }
    return entry.bbox;
  }

  /** Nodes whose effective world bbox contains the point (conservative). */
  candidates(x: number, y: number, view: TreeView = liveView()): SceneNode[] {
    const out: SceneNode[] = [];
    const visit = (node: SceneNode): void => {
      if (view.matrixOf(node) === undefined) return;
      const bbox = this.bboxOf(node, view);
      if (bbox && bboxContains(bbox, x, y)) out.push(node);
      for (const child of view.childrenOf(node)) visit(child);
    };
    visit(this.scene.root);
    return out;
  }

  /**
   * Topmost exact hit among the candidates. Candidates are re-checked
   * with local geometry and ancestor clipping, then ordered by the same
   * reverse traversal the brute-force `hitTest` uses.
   */
  hit(x: number, y: number, view: TreeView = liveView()): SceneNode | null {
    const candidates = this.candidates(x, y, view);
    if (candidates.length === 0) return null;
    const ranks = zOrderRanks(this.scene.root, view);
    let best: SceneNode | null = null;
    let bestRank = Infinity;
    for (const node of candidates) {
      if (!exactHit(node, x, y, view)) continue;
      const rank = ranks.get(node.id);
      if (rank !== undefined && rank < bestRank) {
        bestRank = rank;
        best = node;
      }
    }
    return best;
  }

  /** Number of live cache entries (includes entries kept for snapshots). */
  get size(): number {
    return this.cache.size;
  }

  clear(): void {
    this.cache.clear();
  }
}
