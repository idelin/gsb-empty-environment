/**
 * Facade tying everything together: versioned chunked storage, edit
 * sessions with incremental repair, and clearance queries.
 */

import { VersionedField } from "./field.ts";
import type { Version, Cell } from "./field.ts";
import { EditSession } from "./session.ts";
import {
  clearanceAt,
  pathClearance,
  isPathClear,
} from "./clearance.ts";
import type { Vec2, PathClearance } from "./clearance.ts";

export class DistanceField {
  private store: VersionedField;
  readonly width: number;
  readonly height: number;
  readonly originX: number;
  readonly originY: number;

  constructor(
    width: number,
    height: number,
    originX = 0,
    originY = 0,
  ) {
    this.width = width;
    this.height = height;
    this.originX = originX;
    this.originY = originY;
    this.store = new VersionedField(width, height, originX, originY);
  }

  /** Build a field and populate it with an initial obstacle set. */
  static fromObstacles(
    width: number,
    height: number,
    obstacles: Iterable<readonly [number, number]>,
    originX = 0,
    originY = 0,
  ): DistanceField {
    const f = new DistanceField(width, height, originX, originY);
    const session = f.beginEdit();
    for (const [x, y] of obstacles) session.setObstacle(x, y, true);
    session.commitFull();
    return f;
  }

  /** The current published (fully consistent) version. */
  get version(): Version {
    return this.store.snapshot();
  }

  beginEdit(): EditSession {
    return new EditSession(this.store);
  }

  /** Pin an old version so it stays queryable. */
  retain(v: Version): Version {
    return this.store.retain(v);
  }

  release(v: Version): void {
    this.store.release(v);
  }

  /** Reclaim chunk pages no longer referenced by any live version. */
  collectGarbage(): number {
    return this.store.collectGarbage();
  }

  get pageCount(): number {
    return this.store.pageCount;
  }

  /** Squared distance + nearest source for a world cell, at a version. */
  cellAt(v: Version, wx: number, wy: number): Cell {
    return this.store.getCell(v, wx, wy);
  }

  clearanceAt(v: Version, wx: number, wy: number): number {
    return clearanceAt(this.store, v, wx, wy);
  }

  pathClearance(v: Version, points: ReadonlyArray<Vec2>, step?: number): PathClearance {
    return pathClearance(this.store, v, points, step);
  }

  isPathClear(v: Version, points: ReadonlyArray<Vec2>, radius: number, step?: number): boolean {
    return isPathClear(this.store, v, points, radius, step);
  }
}
