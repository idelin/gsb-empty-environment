/**
 * Drag snapping engine.
 *
 * Coordinates handed to {@link SnapEngine.snap} are integer subpixels;
 * `scale` subpixels make one logical pixel. Reference lines live in
 * logical-pixel space and may sit at fractional positions. All
 * thresholds are expressed in logical pixels.
 *
 * Behaviour:
 * - A free axis snaps to the nearest line whose distance is within
 *   `snapThreshold`; ties on distance are broken by ascending line id.
 * - An engaged axis keeps its line while the distance stays within the
 *   (larger) `releaseThreshold`, which suppresses jitter around the
 *   engage boundary. The snap is sticky: a merely-closer line does not
 *   steal an engagement.
 * - Deleting the engaged line releases the axis immediately.
 * - Horizontal and vertical snapping are independent and may engage on
 *   the same pointer position.
 */

export type Orientation = 'vertical' | 'horizontal';

export interface ReferenceLine {
  readonly id: number;
  /** A vertical line snaps the x coordinate, a horizontal line snaps y. */
  readonly orientation: Orientation;
  /** Line position in logical pixels (fractions allowed). */
  readonly position: number;
}

export interface SnapEngineOptions {
  /** Distance in logical pixels within which a free axis engages. */
  readonly snapThreshold: number;
  /**
   * Distance in logical pixels within which an engaged axis stays
   * snapped. Should be >= `snapThreshold`.
   */
  readonly releaseThreshold: number;
  /** Subpixels per logical pixel. Must be a positive integer. */
  readonly scale: number;
}

export interface Point {
  /** Integer subpixels. */
  readonly x: number;
  readonly y: number;
}

export interface AxisSnap {
  readonly lineId: number;
  /** Snapped coordinate in integer subpixels. */
  readonly value: number;
  /** Distance from the raw position to the line, in logical pixels. */
  readonly distance: number;
}

export interface SnapResult {
  /** Output coordinates in integer subpixels. */
  readonly x: number;
  readonly y: number;
  /** Engagement on the x axis (a vertical reference line), if any. */
  readonly xSnap: AxisSnap | null;
  /** Engagement on the y axis (a horizontal reference line), if any. */
  readonly ySnap: AxisSnap | null;
}

type Axis = 'x' | 'y';

const AXIS_ORIENTATION: Record<Axis, Orientation> = {
  x: 'vertical',
  y: 'horizontal',
};

export class SnapEngine {
  private snapThreshold: number;
  private releaseThreshold: number;
  private scale: number;

  private readonly lines = new Map<number, ReferenceLine>();
  private readonly engaged: Record<Axis, number | null> = { x: null, y: null };

  constructor(options: SnapEngineOptions) {
    if (!Number.isInteger(options.scale) || options.scale < 1) {
      throw new RangeError('scale must be a positive integer');
    }
    if (options.snapThreshold < 0 || options.releaseThreshold < 0) {
      throw new RangeError('thresholds must be non-negative');
    }
    if (options.releaseThreshold < options.snapThreshold) {
      throw new RangeError('releaseThreshold must be >= snapThreshold');
    }
    this.snapThreshold = options.snapThreshold;
    this.releaseThreshold = options.releaseThreshold;
    this.scale = options.scale;
  }

  /** Subpixels per logical pixel. */
  getScale(): number {
    return this.scale;
  }

  setScale(scale: number): void {
    if (!Number.isInteger(scale) || scale < 1) {
      throw new RangeError('scale must be a positive integer');
    }
    this.scale = scale;
  }

  setThresholds(snapThreshold: number, releaseThreshold: number): void {
    if (snapThreshold < 0 || releaseThreshold < 0) {
      throw new RangeError('thresholds must be non-negative');
    }
    if (releaseThreshold < snapThreshold) {
      throw new RangeError('releaseThreshold must be >= snapThreshold');
    }
    this.snapThreshold = snapThreshold;
    this.releaseThreshold = releaseThreshold;
  }

  /** Adds a line or replaces the line with the same id. */
  upsertLine(line: ReferenceLine): void {
    this.lines.set(line.id, { ...line });
  }

  /** Moves a line, keeping any engagement on it. No-op for unknown ids. */
  moveLine(id: number, position: number): void {
    const line = this.lines.get(id);
    if (line !== undefined) {
      this.lines.set(id, { ...line, position });
    }
  }

  /**
   * Removes a line. If either axis was snapped to it, that engagement
   * is released immediately.
   */
  removeLine(id: number): void {
    this.lines.delete(id);
    for (const axis of ['x', 'y'] as const) {
      if (this.engaged[axis] === id) {
        this.engaged[axis] = null;
      }
    }
  }

  clearLines(): void {
    this.lines.clear();
    this.engaged.x = null;
    this.engaged.y = null;
  }

  /** Drops both engagements without touching the line set. */
  resetEngagement(): void {
    this.engaged.x = null;
    this.engaged.y = null;
  }

  /** Id of the line the axis is currently snapped to, if any. */
  engagedLine(axis: Axis): number | null {
    return this.engaged[axis];
  }

  /**
   * Snaps a pointer position given in integer subpixels. Call it on
   * every pointer move; the engine keeps per-axis engagement state
   * between calls to provide hysteresis.
   */
  snap(point: Point): SnapResult {
    const xSnap = this.snapAxis('x', point.x);
    const ySnap = this.snapAxis('y', point.y);
    return {
      x: xSnap === null ? point.x : xSnap.value,
      y: ySnap === null ? point.y : ySnap.value,
      xSnap,
      ySnap,
    };
  }

  private snapAxis(axis: Axis, subpixel: number): AxisSnap | null {
    const position = subpixel / this.scale;

    const engagedId = this.engaged[axis];
    if (engagedId !== null) {
      const line = this.lines.get(engagedId);
      if (line !== undefined) {
        const distance = Math.abs(position - line.position);
        if (distance <= this.releaseThreshold) {
          return this.toSnap(line, distance);
        }
      }
      // Line gone or dragged out of the release threshold: let go and
      // fall through to a fresh scan.
      this.engaged[axis] = null;
    }

    const orientation = AXIS_ORIENTATION[axis];
    let best: ReferenceLine | null = null;
    let bestDistance = Infinity;
    for (const line of this.lines.values()) {
      if (line.orientation !== orientation) {
        continue;
      }
      const distance = Math.abs(position - line.position);
      if (
        distance < bestDistance ||
        (distance === bestDistance && best !== null && line.id < best.id)
      ) {
        best = line;
        bestDistance = distance;
      }
    }

    if (best !== null && bestDistance <= this.snapThreshold) {
      this.engaged[axis] = best.id;
      return this.toSnap(best, bestDistance);
    }
    return null;
  }

  private toSnap(line: ReferenceLine, distance: number): AxisSnap {
    return {
      lineId: line.id,
      value: Math.round(line.position * this.scale),
      distance,
    };
  }
}
