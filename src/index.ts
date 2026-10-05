import { BVH } from "./bvh.ts";
import { intersectSegmentBox } from "./intersect.ts";
import { mergeHits } from "./merge.ts";
import { AXES } from "./types.ts";
import type {
  ObjectResult,
  QueryResult,
  QueryStats,
  Segment,
  SegmentHit,
  SpatioTemporalQuery,
} from "./types.ts";

export class InvalidSegmentError extends Error {}

export interface Snapshot {
  version: number;
  segments: readonly Segment[];
}

interface ObjectVersion extends Snapshot {
  bvh: BVH;
}

interface ObjectTrajectory {
  versions: ObjectVersion[]; // ascending by version
}

function emptyStats(): QueryStats {
  return {
    bvhNodesVisited: 0,
    bvhLeavesVisited: 0,
    candidateSegments: 0,
    exactIntersectionTests: 0,
    hitSegments: 0,
    totalSegments: 0,
  };
}

function assertSafeInteger(value: number, what: string): void {
  if (!Number.isSafeInteger(value)) {
    throw new InvalidSegmentError(`${what} must be a safe integer, got ${value}`);
  }
}

function validateSegment(seg: Segment): void {
  assertSafeInteger(seg.t0, `segment ${seg.segmentId}: t0`);
  assertSafeInteger(seg.t1, `segment ${seg.segmentId}: t1`);
  if (seg.t1 <= seg.t0) {
    throw new InvalidSegmentError(
      `segment ${seg.segmentId}: illegal zero/negative duration [${seg.t0}, ${seg.t1})`,
    );
  }
  for (const axis of AXES) {
    assertSafeInteger(seg.from[axis], `segment ${seg.segmentId}: from.${axis}`);
    assertSafeInteger(seg.to[axis], `segment ${seg.segmentId}: to.${axis}`);
  }
}

function validateQuery(query: SpatioTemporalQuery): void {
  if (
    !Number.isSafeInteger(query.time.t0) ||
    !Number.isSafeInteger(query.time.t1) ||
    query.time.t1 <= query.time.t0
  ) {
    throw new Error(
      `query time range must be non-empty [t0, t1), got [${query.time.t0}, ${query.time.t1})`,
    );
  }
  for (const axis of AXES) {
    if (query.box.min[axis] > query.box.max[axis]) {
      throw new Error(`query box: min.${axis} > max.${axis}`);
    }
  }
}

/**
 * Versioned spatiotemporal index. Each trajectory update creates a new
 * immutable global version; queries may target any past version and
 * resolve, per object, the latest snapshot at or before that version.
 */
export class SpatioTemporalIndex {
  private objects = new Map<string, ObjectTrajectory>();
  private currentVersion = 0;
  private cumulative = emptyStats();

  /** Replace an object's trajectory; returns the new global version. */
  updateTrajectory(objectId: string, segments: Segment[]): number {
    for (const seg of segments) {
      if (seg.objectId !== objectId) {
        throw new InvalidSegmentError(
          `segment ${seg.segmentId}: objectId ${seg.objectId} does not match update target ${objectId}`,
        );
      }
      validateSegment(seg);
    }
    const ids = new Set<string>();
    for (const seg of segments) {
      if (ids.has(seg.segmentId)) {
        throw new InvalidSegmentError(
          `duplicate segmentId ${seg.segmentId} in update for ${objectId}`,
        );
      }
      ids.add(seg.segmentId);
    }
    const version = ++this.currentVersion;
    const stored = segments.map((s) => ({
      ...s,
      from: { ...s.from },
      to: { ...s.to },
    }));
    let traj = this.objects.get(objectId);
    if (!traj) {
      traj = { versions: [] };
      this.objects.set(objectId, traj);
    }
    traj.versions.push({ version, segments: stored, bvh: BVH.build(stored) });
    return version;
  }

  /** Latest global version (number of updates applied so far). */
  getVersion(): number {
    return this.currentVersion;
  }

  getObjectIds(): string[] {
    return [...this.objects.keys()];
  }

  /**
   * Resolve an object's snapshot at a global version: the object's latest
   * version <= version, or null if the object did not exist yet.
   */
  getSnapshot(objectId: string, version: number): Snapshot | null {
    const traj = this.objects.get(objectId);
    if (!traj) return null;
    let found: ObjectVersion | null = null;
    for (const v of traj.versions) {
      if (v.version <= version) found = v;
      else break;
    }
    return found;
  }

  /** Cumulative access statistics across all queries. */
  getStats(): QueryStats {
    return { ...this.cumulative };
  }

  /** Query the latest version. */
  query(query: SpatioTemporalQuery, version?: number): QueryResult {
    return this.runQuery(query, version ?? this.currentVersion, true);
  }

  /**
   * Reference implementation: exact intersection over every segment of the
   * resolved snapshots, no BVH pruning. Used to cross-check `query`.
   */
  queryBruteForce(query: SpatioTemporalQuery, version?: number): QueryResult {
    return this.runQuery(query, version ?? this.currentVersion, false);
  }

  private runQuery(
    query: SpatioTemporalQuery,
    version: number,
    useBvh: boolean,
  ): QueryResult {
    validateQuery(query);
    const stats = emptyStats();
    const objects: ObjectResult[] = [];
    for (const [objectId, traj] of this.objects) {
      let snap: ObjectVersion | null = null;
      for (const v of traj.versions) {
        if (v.version <= version) snap = v;
        else break;
      }
      if (!snap) continue;
      stats.totalSegments += snap.segments.length;
      let candidates: readonly Segment[];
      if (useBvh) {
        const collected: Segment[] = [];
        snap.bvh.collectCandidates(query, stats, collected);
        candidates = collected;
      } else {
        candidates = snap.segments;
        stats.candidateSegments += candidates.length;
      }
      const hits: SegmentHit[] = [];
      for (const seg of candidates) {
        stats.exactIntersectionTests++;
        const interval = intersectSegmentBox(seg, query.box, query.time);
        if (interval) hits.push({ segmentId: seg.segmentId, interval });
      }
      stats.hitSegments += hits.length;
      if (hits.length > 0) {
        objects.push({
          objectId,
          version: snap.version,
          intervals: mergeHits(hits),
        });
      }
    }
    for (const key of Object.keys(stats) as (keyof QueryStats)[]) {
      this.cumulative[key] += stats[key];
    }
    return { version, objects, stats };
  }
}
