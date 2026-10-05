// SpatioTemporalIndex: versioned storage + BVH-pruned exact queries.
//
// Usage:
//   const idx = new SpatioTemporalIndex();
//   idx.addSegment({...});            // stage into the working set
//   const v1 = idx.commit();          // freeze as version 1
//   idx.updateObject('obj', [...]);   // stage a new trajectory for 'obj'
//   const v2 = idx.commit();          // freeze as version 2
//   idx.query(box, window);           // queries latest version (v2)
//   idx.query(box, window, { version: v1 });  // old snapshot still queryable
//
// Every query result carries the version id it was computed from, so a hit
// can always be traced back to the exact trajectory version and segments.

import { fracToString } from './fraction.js';
import type { Fraction } from './fraction.js';
import {
  normalizeSegment,
  normalizeBox,
  normalizeWindow,
} from './geometry.js';
import type {
  Segment,
  SegmentInput,
  Box3,
  Box3Input,
  TimeWindow,
  TimeWindowInput,
} from './geometry.js';
import { intersectSegmentBox } from './intersect.js';
import type { MergedInterval, HitSource } from './intersect.js';
import { mergeIntervals } from './merge.js';
import { BVHTree } from './bvh.js';

export interface QueryOptions {
  readonly version?: number;
}

export interface ObjectHits {
  readonly objectId: string;
  readonly version: number;
  readonly intervals: MergedInterval[];
}

export interface QueryStats {
  version: number;
  totalSegments: number;
  nodesVisited: number;
  nodesPruned: number;
  leavesVisited: number;
  candidateSegments: number;
  prunedSegments: number;
  exactTests: number;
  hitSegments: number;
}

export interface QueryResult {
  readonly version: number;
  readonly window: TimeWindow;
  readonly box: Box3;
  readonly objects: ObjectHits[];
  readonly stats: QueryStats;
}

interface Snapshot {
  id: number;
  segments: Segment[];
  byObject: Map<string, Segment[]>;
  tree: BVHTree | null;
}

export class SpatioTemporalIndex {
  _working: Map<string, Segment> = new Map();
  _versions: Snapshot[] = [];
  lastQueryStats: QueryStats | null = null;

  addSegment(input: SegmentInput): void {
    const seg = normalizeSegment(input);
    if (this._working.has(seg.segmentId)) {
      throw new Error('duplicate segmentId in working set: ' + seg.segmentId);
    }
    this._working.set(seg.segmentId, seg);
  }

  addSegments(inputs: SegmentInput[]): void {
    for (const input of inputs) {
      this.addSegment(input);
    }
  }

  removeObject(objectId: string): number {
    const doomed: string[] = [];
    for (const seg of this._working.values()) {
      if (seg.objectId === objectId) {
        doomed.push(seg.segmentId);
      }
    }
    for (const id of doomed) {
      this._working.delete(id);
    }
    return doomed.length;
  }

  updateObject(objectId: string, inputs: SegmentInput[]): void {
    const normalized: SegmentInput[] = [];
    for (const input of inputs) {
      normalizeSegment(input);
      normalized.push(input);
    }
    this.removeObject(objectId);
    this.addSegments(normalized);
  }

  commit(): number {
    const segments: Segment[] = [];
    for (const seg of this._working.values()) {
      segments.push(seg);
    }
    const byObject: Map<string, Segment[]> = new Map();
    for (const seg of segments) {
      const arr = byObject.get(seg.objectId);
      if (arr === undefined) {
        byObject.set(seg.objectId, [seg]);
      } else {
        arr.push(seg);
      }
    }
    const id = this._versions.length + 1;
    this._versions.push({ id: id, segments: segments, byObject: byObject, tree: null });
    return id;
  }

  versionCount(): number {
    return this._versions.length;
  }

  listVersions(): number[] {
    const out: number[] = [];
    for (const snap of this._versions) {
      out.push(snap.id);
    }
    return out;
  }

  resolveVersion(version: number | null): Snapshot {
    if (this._versions.length === 0) {
      throw new Error('no committed versions; call commit() before querying');
    }
    if (version === null) {
      return this._versions[this._versions.length - 1];
    }
    for (const snap of this._versions) {
      if (snap.id === version) {
        return snap;
      }
    }
    throw new Error('unknown version ' + version + '; committed versions: ' + this.listVersions().join(', '));
  }

  getVersionSegments(version: number): Segment[] {
    return this.resolveVersion(version).segments;
  }

  query(boxInput: Box3Input, windowInput: TimeWindowInput, options: QueryOptions = {}): QueryResult {
    const box = normalizeBox(boxInput);
    const win = normalizeWindow(windowInput);
    const requested = options.version === undefined ? null : options.version;
    const snap = this.resolveVersion(requested);
    if (snap.tree === null) {
      snap.tree = new BVHTree(snap.segments);
    }
    const tree = snap.tree;
    const stats: QueryStats = {
      version: snap.id,
      totalSegments: snap.segments.length,
      nodesVisited: 0,
      nodesPruned: 0,
      leavesVisited: 0,
      candidateSegments: 0,
      prunedSegments: 0,
      exactTests: 0,
      hitSegments: 0,
    };
    const candidates = tree.query(box, win, stats);
    stats.exactTests = candidates.length;
    const perObject: Map<string, HitSource[]> = new Map();
    for (const seg of candidates) {
      const interval = intersectSegmentBox(seg, box, win);
      if (interval === null) {
        continue;
      }
      stats.hitSegments++;
      const source: HitSource = { segmentId: seg.segmentId, interval: interval };
      const list = perObject.get(seg.objectId);
      if (list === undefined) {
        perObject.set(seg.objectId, [source]);
      } else {
        list.push(source);
      }
    }
    stats.prunedSegments = stats.totalSegments - stats.candidateSegments;
    const objects: ObjectHits[] = [];
    for (const entry of perObject) {
      const objectId = entry[0];
      const sources = entry[1];
      objects.push({ objectId: objectId, version: snap.id, intervals: mergeIntervals(sources) });
    }
    objects.sort((a, b) => (a.objectId < b.objectId ? -1 : a.objectId > b.objectId ? 1 : 0));
    const result: QueryResult = { version: snap.id, window: win, box: box, objects: objects, stats: stats };
    this.lastQueryStats = stats;
    return result;
  }
}

function formatIntervalParts(start: Fraction, end: Fraction, endInclusive: boolean) {
  return { start: fracToString(start), end: fracToString(end), endInclusive: endInclusive };
}

export function formatQueryResult(result: QueryResult) {
  return {
    version: result.version,
    window: { t0: result.window.t0.toString(), t1: result.window.t1.toString() },
    box: {
      lo: { x: result.box.lo.x.toString(), y: result.box.lo.y.toString(), z: result.box.lo.z.toString() },
      hi: { x: result.box.hi.x.toString(), y: result.box.hi.y.toString(), z: result.box.hi.z.toString() },
    },
    objects: result.objects.map((o) => ({
      objectId: o.objectId,
      version: o.version,
      intervals: o.intervals.map((iv) => ({
        start: fracToString(iv.start),
        end: fracToString(iv.end),
        endInclusive: iv.endInclusive,
        sources: iv.sources.map((s) => ({
          segmentId: s.segmentId,
          interval: formatIntervalParts(s.interval.start, s.interval.end, s.interval.endInclusive),
        })),
      })),
    })),
    stats: result.stats,
  };
}
