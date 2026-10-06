export type LineId = string | number;

export type Axis = 'horizontal' | 'vertical';

export interface GuideLine {
  id: LineId;
  /** 'vertical' lines snap the x coordinate; 'horizontal' lines snap the y coordinate. */
  axis: Axis;
  /** Line coordinate in integer subpixels. */
  position: number;
}

export interface SnapperOptions {
  /** Distance (screen px) within which a drag point snaps to the nearest line. */
  snapThresholdPx: number;
  /** Distance (screen px) beyond which an active snap is released. Must be >= snapThresholdPx. */
  releaseThresholdPx: number;
  /** Subpixel units per logical pixel for all input coordinates. Defaults to 64. */
  subpixelsPerPixel?: number;
  /** Screen px per logical px. Defaults to 1. */
  zoom?: number;
}

export interface AxisSnapResult {
  /** Resulting coordinate in integer subpixels (exact line position when snapped). */
  position: number;
  snapped: boolean;
  lineId: LineId | null;
}

export interface SnapResult {
  x: AxisSnapResult;
  y: AxisSnapResult;
}

interface AxisState {
  axis: Axis;
  snappedLineId: LineId | null;
}

function compareLineIds(a: LineId, b: LineId): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  const sa = String(a);
  const sb = String(b);
  return sa < sb ? -1 : sa > sb ? 1 : 0;
}

function assertInteger(value: number, name: string): void {
  if (!Number.isInteger(value)) {
    throw new TypeError(`${name} must be an integer number of subpixels, got ${value}`);
  }
}

/**
 * Drag snapping engine with hysteresis.
 *
 * All coordinates are integer subpixels (see `subpixelsPerPixel`). Thresholds
 * are screen pixels and are compared against `|drag - line| / subpixelsPerPixel * zoom`,
 * so the effective logical snap range shrinks as zoom increases.
 *
 * Per axis state machine:
 * - Unsnapped: snap to the nearest line within `snapThresholdPx`; equal
 *   distances are broken by line id (ascending).
 * - Snapped: stay snapped while within `releaseThresholdPx` of the same line
 *   (the larger release threshold prevents jitter at the snap boundary).
 *   The snap follows the line if it moves, and is released immediately when
 *   the line is removed.
 */
export class Snapper {
  private readonly lines = new Map<LineId, GuideLine>();
  private readonly snapThresholdPx: number;
  private readonly releaseThresholdPx: number;
  private readonly subpixelsPerPixel: number;
  private zoom: number;
  private readonly axes: Record<Axis, AxisState> = {
    vertical: { axis: 'vertical', snappedLineId: null },
    horizontal: { axis: 'horizontal', snappedLineId: null },
  };

  constructor(options: SnapperOptions) {
    const { snapThresholdPx, releaseThresholdPx } = options;
    const subpixelsPerPixel = options.subpixelsPerPixel ?? 64;
    const zoom = options.zoom ?? 1;
    if (!(snapThresholdPx >= 0)) throw new RangeError('snapThresholdPx must be >= 0');
    if (!(releaseThresholdPx >= snapThresholdPx)) {
      throw new RangeError('releaseThresholdPx must be >= snapThresholdPx');
    }
    if (!(subpixelsPerPixel > 0)) throw new RangeError('subpixelsPerPixel must be > 0');
    if (!(zoom > 0)) throw new RangeError('zoom must be > 0');
    this.snapThresholdPx = snapThresholdPx;
    this.releaseThresholdPx = releaseThresholdPx;
    this.subpixelsPerPixel = subpixelsPerPixel;
    this.zoom = zoom;
  }

  setZoom(zoom: number): void {
    if (!(zoom > 0)) throw new RangeError('zoom must be > 0');
    this.zoom = zoom;
  }

  getZoom(): number {
    return this.zoom;
  }

  addLine(line: GuideLine): void {
    assertInteger(line.position, 'line position');
    this.lines.set(line.id, { ...line });
  }

  moveLine(id: LineId, position: number): void {
    assertInteger(position, 'line position');
    const line = this.lines.get(id);
    if (line === undefined) throw new Error(`unknown line id: ${String(id)}`);
    line.position = position;
  }

  /** Removes the line and synchronously releases any snap bound to it. */
  removeLine(id: LineId): void {
    this.lines.delete(id);
    if (this.axes.vertical.snappedLineId === id) this.axes.vertical.snappedLineId = null;
    if (this.axes.horizontal.snappedLineId === id) this.axes.horizontal.snappedLineId = null;
  }

  clearLines(): void {
    this.lines.clear();
    this.axes.vertical.snappedLineId = null;
    this.axes.horizontal.snappedLineId = null;
  }

  /** Line currently snapped on the given axis, or null. */
  snappedLineId(axis: Axis): LineId | null {
    return this.axes[axis].snappedLineId;
  }

  /** Evaluates snapping for a drag point; x and y axes are handled independently. */
  update(x: number, y: number): SnapResult {
    assertInteger(x, 'x');
    assertInteger(y, 'y');
    return {
      x: this.resolveAxis(this.axes.vertical, x),
      y: this.resolveAxis(this.axes.horizontal, y),
    };
  }

  private screenDistance(aSub: number, bSub: number): number {
    return (Math.abs(aSub - bSub) / this.subpixelsPerPixel) * this.zoom;
  }

  private resolveAxis(state: AxisState, raw: number): AxisSnapResult {
    if (state.snappedLineId !== null) {
      const current = this.lines.get(state.snappedLineId);
      if (current === undefined) {
        state.snappedLineId = null;
      } else if (this.screenDistance(raw, current.position) <= this.releaseThresholdPx) {
        return { position: current.position, snapped: true, lineId: current.id };
      } else {
        state.snappedLineId = null;
      }
    }

    let best: GuideLine | null = null;
    let bestDistance = Infinity;
    for (const line of this.lines.values()) {
      if (line.axis !== state.axis) continue;
      const distance = this.screenDistance(raw, line.position);
      if (
        distance < bestDistance ||
        (distance === bestDistance && best !== null && compareLineIds(line.id, best.id) < 0)
      ) {
        best = line;
        bestDistance = distance;
      }
    }

    if (best !== null && bestDistance <= this.snapThresholdPx) {
      state.snappedLineId = best.id;
      return { position: best.position, snapped: true, lineId: best.id };
    }
    return { position: raw, snapped: false, lineId: null };
  }
}
