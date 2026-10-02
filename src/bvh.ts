import { AABB4 } from './aabb4.ts';
import { segmentEntersBox, type TrajectorySegment } from './segment.ts';

const DEFAULT_LEAF_CAPACITY = 4;

interface BVHNode {
  bounds: AABB4;
  left: BVHNode | null;
  right: BVHNode | null;
  /** Non-null only on leaves. */
  segments: TrajectorySegment[] | null;
}

export interface QueryStats {
  /** Internal + leaf nodes touched during traversal. */
  nodesVisited: number;
  /** Leaf segments whose AABB survived pruning and were tested. */
  candidatesTested: number;
  /** Wall-clock time of the query in milliseconds. */
  elapsedMs: number;
}

export interface QueryResult {
  segments: TrajectorySegment[];
  stats: QueryStats;
}

/**
 * 4D (3D + time) Bounding Volume Hierarchy over trajectory segments.
 *
 * - Bulk build: top-down median split on the widest axis (O(n log n)).
 * - Dynamic insert: descend by least hyper-volume enlargement, split
 *   overflowing leaves, refit ancestor bounds.
 * - Query: classic BVH pruning — a node is skipped entirely when its 4D
 *   bounds do not overlap the query box, so whole subtrees of irrelevant
 *   space-time are discarded in one comparison.
 */
export class SpaceTimeBVH {
  private root: BVHNode | null = null;
  private readonly leafCapacity: number;
  private segmentCount = 0;

  constructor(segments: TrajectorySegment[] = [], leafCapacity = DEFAULT_LEAF_CAPACITY) {
    this.leafCapacity = leafCapacity;
    if (segments.length > 0) {
      this.root = this.build(segments);
      this.segmentCount = segments.length;
    }
  }

  get size(): number {
    return this.segmentCount;
  }

  private build(segments: TrajectorySegment[]): BVHNode {
    if (segments.length <= this.leafCapacity) {
      return this.makeLeaf(segments);
    }
    const axis = centroidBounds(segments).widestAxis();
    const sorted = [...segments].sort(
      (a, b) => a.bounds.centroid(axis) - b.bounds.centroid(axis),
    );
    const mid = Math.floor(sorted.length / 2);
    const left = this.build(sorted.slice(0, mid));
    const right = this.build(sorted.slice(mid));
    return this.makeInternal(left, right);
  }

  private makeLeaf(segments: TrajectorySegment[]): BVHNode {
    const bounds = AABB4.empty();
    for (const seg of segments) bounds.expandToInclude(seg.bounds);
    return { bounds, left: null, right: null, segments: [...segments] };
  }

  private makeInternal(left: BVHNode, right: BVHNode): BVHNode {
    return {
      bounds: left.bounds.expanded(right.bounds),
      left,
      right,
      segments: null,
    };
  }

  /** Insert one segment; the tree stays balanced via leaf splitting. */
  insert(segment: TrajectorySegment): void {
    this.segmentCount++;
    if (this.root === null) {
      this.root = this.makeLeaf([segment]);
      return;
    }
    this.root = this.insertInto(this.root, segment);
  }

  /** Bulk-load many segments (rebuild from scratch — cheapest for batches). */
  rebuild(segments: TrajectorySegment[]): void {
    this.segmentCount = segments.length;
    this.root = segments.length > 0 ? this.build(segments) : null;
  }

  private insertInto(node: BVHNode, segment: TrajectorySegment): BVHNode {
    if (node.segments !== null) {
      node.segments.push(segment);
      node.bounds.expandToInclude(segment.bounds);
      if (node.segments.length > this.leafCapacity) {
        return this.build(node.segments); // split overflowing leaf
      }
      return node;
    }
    const leftCost = node.left!.bounds.enlargement(segment.bounds);
    const rightCost = node.right!.bounds.enlargement(segment.bounds);
    if (leftCost <= rightCost) {
      node.left = this.insertInto(node.left!, segment);
    } else {
      node.right = this.insertInto(node.right!, segment);
    }
    node.bounds.expandToInclude(segment.bounds);
    return node;
  }

  /**
   * Find all segments overlapping the query box.
   *
   * @param box   4D query box (time range + spatial cube).
   * @param exact When true (default), candidates are refined with an exact
   *              segment-vs-box intersection test, eliminating AABB false
   *              positives so only segments that truly enter the box during
   *              the time window are returned.
   */
  query(box: AABB4, exact = true): QueryResult {
    const started = performance.now();
    const stats: QueryStats = { nodesVisited: 0, candidatesTested: 0, elapsedMs: 0 };
    const hits: TrajectorySegment[] = [];
    if (this.root !== null) {
      this.queryNode(this.root, box, exact, hits, stats);
    }
    stats.elapsedMs = performance.now() - started;
    return { segments: hits, stats };
  }

  private queryNode(
    node: BVHNode,
    box: AABB4,
    exact: boolean,
    hits: TrajectorySegment[],
    stats: QueryStats,
  ): void {
    stats.nodesVisited++;
    if (!node.bounds.intersects(box)) return; // prune the whole subtree
    if (node.segments !== null) {
      for (const seg of node.segments) {
        if (!seg.bounds.intersects(box)) continue;
        stats.candidatesTested++;
        if (!exact || segmentEntersBox(seg, box)) {
          hits.push(seg);
        }
      }
      return;
    }
    this.queryNode(node.left!, box, exact, hits, stats);
    this.queryNode(node.right!, box, exact, hits, stats);
  }

  /** Total number of tree nodes (for diagnostics / pruning ratio checks). */
  nodeCount(): number {
    let count = 0;
    const walk = (node: BVHNode | null): void => {
      if (node === null) return;
      count++;
      walk(node.left);
      walk(node.right);
    };
    walk(this.root);
    return count;
  }
}

function centroidBounds(segments: TrajectorySegment[]): AABB4 {
  const bounds = AABB4.empty();
  for (const seg of segments) {
    const c = AABB4.empty();
    for (let axis = 0; axis < 4; axis++) {
      const v = seg.bounds.centroid(axis);
      // Reuse expandToInclude via a degenerate box at the centroid.
      switch (axis) {
        case 0: c.minX = c.maxX = v; break;
        case 1: c.minY = c.maxY = v; break;
        case 2: c.minZ = c.maxZ = v; break;
        default: c.minT = c.maxT = v;
      }
    }
    bounds.expandToInclude(c);
  }
  return bounds;
}
