// BVH over 4D (x, y, z, t) segment bounds. The BVH only prunes candidates;
// it never decides hits. Final results always come from the exact rational
// intersection in intersect.ts. A segment whose 4D bounding box overlaps
// the query but which never actually enters the box is a false candidate
// and is filtered out by the exact test.

import type { Segment, Box3, TimeWindow } from './geometry.js';

export interface Bounds4 {
  loX: bigint;
  hiX: bigint;
  loY: bigint;
  hiY: bigint;
  loZ: bigint;
  hiZ: bigint;
  loT: bigint;
  hiT: bigint;
}

interface Item {
  seg: Segment;
  b: Bounds4;
}

interface BVHNode {
  bounds: Bounds4;
  left: BVHNode | null;
  right: BVHNode | null;
  leaf: Item[] | null;
}

export interface BVHStats {
  nodesVisited: number;
  nodesPruned: number;
  leavesVisited: number;
  candidateSegments: number;
}

export function segmentBounds(s: Segment): Bounds4 {
  return {
    loX: s.p0.x < s.p1.x ? s.p0.x : s.p1.x,
    hiX: s.p0.x < s.p1.x ? s.p1.x : s.p0.x,
    loY: s.p0.y < s.p1.y ? s.p0.y : s.p1.y,
    hiY: s.p0.y < s.p1.y ? s.p1.y : s.p0.y,
    loZ: s.p0.z < s.p1.z ? s.p0.z : s.p1.z,
    hiZ: s.p0.z < s.p1.z ? s.p1.z : s.p0.z,
    loT: s.t0,
    hiT: s.t1,
  };
}

function unionBounds(a: Bounds4, b: Bounds4): Bounds4 {
  return {
    loX: a.loX < b.loX ? a.loX : b.loX,
    hiX: a.hiX > b.hiX ? a.hiX : b.hiX,
    loY: a.loY < b.loY ? a.loY : b.loY,
    hiY: a.hiY > b.hiY ? a.hiY : b.hiY,
    loZ: a.loZ < b.loZ ? a.loZ : b.loZ,
    hiZ: a.hiZ > b.hiZ ? a.hiZ : b.hiZ,
    loT: a.loT < b.loT ? a.loT : b.loT,
    hiT: a.hiT > b.hiT ? a.hiT : b.hiT,
  };
}

// Closed box vs half-open time window overlap test.
export function boundsOverlapQuery(b: Bounds4, box: Box3, win: TimeWindow): boolean {
  return (
    b.loX <= box.hi.x &&
    box.lo.x <= b.hiX &&
    b.loY <= box.hi.y &&
    box.lo.y <= b.hiY &&
    b.loZ <= box.hi.z &&
    box.lo.z <= b.hiZ &&
    b.loT < win.t1 &&
    win.t0 < b.hiT
  );
}

function centroid(b: Bounds4, axis: number): bigint {
  if (axis === 0) {
    return (b.loX + b.hiX) / 2n;
  }
  if (axis === 1) {
    return (b.loY + b.hiY) / 2n;
  }
  if (axis === 2) {
    return (b.loZ + b.hiZ) / 2n;
  }
  return (b.loT + b.hiT) / 2n;
}

export class BVHTree {
  root: BVHNode | null = null;
  nodeCount: number = 0;
  leafSize: number;

  constructor(segments: Segment[], leafSize: number = 4) {
    this.leafSize = leafSize;
    const items: Item[] = [];
    for (const s of segments) {
      items.push({ seg: s, b: segmentBounds(s) });
    }
    this.root = items.length === 0 ? null : this.build(items);
  }

  build(items: Item[]): BVHNode {
    this.nodeCount++;
    let bounds = items[0].b;
    for (let i = 1; i < items.length; i++) {
      bounds = unionBounds(bounds, items[i].b);
    }
    if (items.length <= this.leafSize) {
      return { bounds: bounds, left: null, right: null, leaf: items };
    }
    const spans = [
      bounds.hiX - bounds.loX,
      bounds.hiY - bounds.loY,
      bounds.hiZ - bounds.loZ,
      bounds.hiT - bounds.loT,
    ];
    let axis = 0;
    for (let i = 1; i < 4; i++) {
      if (spans[i] > spans[axis]) {
        axis = i;
      }
    }
    items.sort((a, b) => {
      const ca = centroid(a.b, axis);
      const cb = centroid(b.b, axis);
      return ca < cb ? -1 : ca > cb ? 1 : 0;
    });
    const mid = items.length >> 1;
    const left = this.build(items.slice(0, mid));
    const right = this.build(items.slice(mid));
    return { bounds: bounds, left: left, right: right, leaf: null };
  }

  query(box: Box3, win: TimeWindow, stats: BVHStats): Segment[] {
    const out: Segment[] = [];
    if (this.root === null) {
      return out;
    }
    const stack: BVHNode[] = [this.root];
    while (stack.length > 0) {
      const node = stack.pop();
      if (node === undefined) {
        break;
      }
      stats.nodesVisited++;
      if (!boundsOverlapQuery(node.bounds, box, win)) {
        stats.nodesPruned++;
        continue;
      }
      if (node.leaf !== null) {
        stats.leavesVisited++;
        for (const item of node.leaf) {
          if (boundsOverlapQuery(item.b, box, win)) {
            stats.candidateSegments++;
            out.push(item.seg);
          }
        }
      } else {
        if (node.left !== null) {
          stack.push(node.left);
        }
        if (node.right !== null) {
          stack.push(node.right);
        }
      }
    }
    return out;
  }
}
