import { AABB4 } from './aabb4.ts';
import { buildSegments, clipSegmentToBox } from './trajectory.ts';
import type {
  ClippedFragment,
  TrajectoryPoint,
  TrajectorySegment,
} from './trajectory.ts';

interface BVHNode {
  bounds: AABB4;
  left: BVHNode | null;
  right: BVHNode | null;
  /** Non-null only on leaves. */
  segments: TrajectorySegment[] | null;
}

/** Optional counters filled in by queries, useful to observe pruning. */
export interface QueryStats {
  nodesVisited: number;
  nodesPruned: number;
  segmentsTested: number;
}

export interface QueryOptions {
  /**
   * When true, segments are additionally verified with exact 4D
   * segment-vs-box clipping (Liang–Barsky), so only trajectories that
   * actually pass through the box are returned.
   */
  precise?: boolean;
  /** Optional object that receives traversal statistics. */
  stats?: QueryStats;
}

const CENTROID_X = (s: TrajectorySegment) => (s.aabb.minX + s.aabb.maxX) / 2;
const CENTROID_Y = (s: TrajectorySegment) => (s.aabb.minY + s.aabb.maxY) / 2;
const CENTROID_Z = (s: TrajectorySegment) => (s.aabb.minZ + s.aabb.maxZ) / 2;
const CENTROID_T = (s: TrajectorySegment) => (s.aabb.minT + s.aabb.maxT) / 2;
const CENTROIDS = [CENTROID_X, CENTROID_Y, CENTROID_Z, CENTROID_T];

function boundsOf(segments: readonly TrajectorySegment[]): AABB4 {
  const box = AABB4.empty();
  for (const seg of segments) box.merge(seg.aabb);
  return box;
}

/** Index of the axis with the widest extent of the given bounds. */
function widestAxis(b: AABB4): number {
  const ex = b.maxX - b.minX;
  const ey = b.maxY - b.minY;
  const ez = b.maxZ - b.minZ;
  const et = b.maxT - b.minT;
  let axis = 0;
  let widest = ex;
  if (ey > widest) { widest = ey; axis = 1; }
  if (ez > widest) { widest = ez; axis = 2; }
  if (et > widest) { axis = 3; }
  return axis;
}

/**
 * BVH (bounding volume hierarchy) over 4D spacetime AABBs.
 *
 * - Bulk-load with {@link SpacetimeBVH.build} for a balanced tree.
 * - Incrementally add data with {@link SpacetimeBVH.insert} /
 *   {@link SpacetimeBVH.insertTrajectory} (top-down insertion with
 *   minimum-volume-enlargement descent and leaf splitting).
 * - Query with a 4D AABB: any branch whose bounds do not overlap the query
 *   box is pruned in O(1), giving sub-linear, millisecond-range lookups.
 */
export class SpacetimeBVH {
  private root: BVHNode | null = null;
  private readonly maxLeafSize: number;
  private segmentCount = 0;

  constructor(maxLeafSize = 8) {
    if (maxLeafSize < 2) throw new Error('maxLeafSize must be >= 2');
    this.maxLeafSize = maxLeafSize;
  }

  get size(): number {
    return this.segmentCount;
  }

  /** Replace the tree content with a freshly bulk-built tree. */
  build(segments: readonly TrajectorySegment[]): void {
    this.segmentCount = segments.length;
    if (segments.length === 0) {
      this.root = null;
      return;
    }
    // Precompute per-axis centroid keys once; nodes then sort integer index
    // ranges by key lookup instead of re-sorting (and copying) segment
    // objects at every level.
    const n = segments.length;
    const keys: Float64Array[] = [
      new Float64Array(n), new Float64Array(n),
      new Float64Array(n), new Float64Array(n),
    ];
    for (let i = 0; i < n; i++) {
      const a = segments[i].aabb;
      keys[0][i] = (a.minX + a.maxX) / 2;
      keys[1][i] = (a.minY + a.maxY) / 2;
      keys[2][i] = (a.minZ + a.maxZ) / 2;
      keys[3][i] = (a.minT + a.maxT) / 2;
    }
    const order = new Uint32Array(n);
    for (let i = 0; i < n; i++) order[i] = i;
    this.root = this.buildNode(segments, keys, order, 0, n);
  }

  private buildNode(
    segments: readonly TrajectorySegment[],
    keys: Float64Array[],
    order: Uint32Array,
    start: number,
    end: number,
  ): BVHNode {
    const bounds = AABB4.empty();
    for (let i = start; i < end; i++) bounds.merge(segments[order[i]].aabb);
    const count = end - start;
    if (count <= this.maxLeafSize) {
      const leaf: TrajectorySegment[] = new Array(count);
      for (let i = 0; i < count; i++) leaf[i] = segments[order[start + i]];
      return { bounds, left: null, right: null, segments: leaf };
    }
    const axis = widestAxis(bounds);
    const key = keys[axis];
    const sorted = Array.from(order.subarray(start, end))
      .sort((a, b) => key[a] - key[b]);
    order.set(sorted, start);
    const mid = start + (count >> 1);
    const left = this.buildNode(segments, keys, order, start, mid);
    const right = this.buildNode(segments, keys, order, mid, end);
    return { bounds, left, right, segments: null };
  }

  /** Insert a single segment (bounds are refit along the insertion path). */
  insert(segment: TrajectorySegment): void {
    this.segmentCount++;
    if (this.root === null) {
      this.root = { bounds: segment.aabb.clone(), left: null, right: null, segments: [segment] };
      return;
    }
    this.insertInto(this.root, segment);
  }

  private insertInto(node: BVHNode, segment: TrajectorySegment): void {
    node.bounds.merge(segment.aabb);
    if (node.segments !== null) {
      node.segments.push(segment);
      if (node.segments.length > this.maxLeafSize) this.splitLeaf(node);
      return;
    }
    // Descend into the child whose bounds would grow the least.
    const leftCost = node.left!.bounds.unionVolume(segment.aabb);
    const rightCost = node.right!.bounds.unionVolume(segment.aabb);
    this.insertInto(leftCost <= rightCost ? node.left! : node.right!, segment);
  }

  private splitLeaf(node: BVHNode): void {
    const segments = node.segments!;
    const axis = widestAxis(node.bounds);
    segments.sort((a, b) => CENTROIDS[axis](a) - CENTROIDS[axis](b));
    const mid = segments.length >> 1;
    const leftSegs = segments.slice(0, mid);
    const rightSegs = segments.slice(mid);
    node.left = { bounds: boundsOf(leftSegs), left: null, right: null, segments: leftSegs };
    node.right = { bounds: boundsOf(rightSegs), left: null, right: null, segments: rightSegs };
    node.segments = null;
  }

  /** Sample a trajectory (id + points) and insert all of its segments. */
  insertTrajectory(trajectoryId: string, points: readonly TrajectoryPoint[]): TrajectorySegment[] {
    const segments = buildSegments(trajectoryId, points);
    for (const seg of segments) this.insert(seg);
    return segments;
  }

  /**
   * Return every segment whose 4D bounding box overlaps the query box.
   * With `precise: true`, only segments that truly pass through the box
   * (exact 4D clipping) are returned.
   */
  query(box: AABB4, options: QueryOptions = {}): TrajectorySegment[] {
    const result: TrajectorySegment[] = [];
    this.traverse(box, options, (seg) => { result.push(seg); });
    return result;
  }

  /**
   * Like {@link query}, but returns the exact in-box fragment (entry/exit
   * points and times) of every segment that passes through the box.
   */
  queryFragments(box: AABB4, options: QueryOptions = {}): ClippedFragment[] {
    const result: ClippedFragment[] = [];
    this.traverse(box, { ...options, precise: true }, (seg) => {
      result.push(clipSegmentToBox(seg, box)!);
    });
    return result;
  }

  private traverse(
    box: AABB4,
    options: QueryOptions,
    emit: (seg: TrajectorySegment) => void,
  ): void {
    if (this.root === null) return;
    const stats = options.stats;
    if (stats) {
      stats.nodesVisited = 0;
      stats.nodesPruned = 0;
      stats.segmentsTested = 0;
    }

    const stack: BVHNode[] = [this.root];
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (stats) stats.nodesVisited++;
      if (!node.bounds.intersects(box)) {
        // Prune the whole subtree: nothing inside can overlap the query box.
        if (stats) stats.nodesPruned++;
        continue;
      }
      if (node.segments !== null) {
        for (const seg of node.segments) {
          if (stats) stats.segmentsTested++;
          if (!seg.aabb.intersects(box)) continue;
          if (options.precise && clipSegmentToBox(seg, box) === null) continue;
          emit(seg);
        }
      } else {
        stack.push(node.left!, node.right!);
      }
    }
  }

  /** Depth of the tree (0 when empty); useful for sanity checks. */
  depth(): number {
    const walk = (node: BVHNode | null): number =>
      node === null ? 0 : 1 + Math.max(walk(node.left), walk(node.right));
    return walk(this.root);
  }
}
