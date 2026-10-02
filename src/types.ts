/** Input event fed into the recognizer. `epoch` identifies the coordinate
 *  space: bump it whenever the coordinate system changes (scroll/resize/
 *  transform) so the recognizer can re-baseline instead of seeing a jump. */
export type PointerEventType = 'down' | 'move' | 'up' | 'cancel' | 'lostcapture';

export interface PointerInput {
  type: PointerEventType;
  pointerId: number;
  x: number;
  y: number;
  /** Timestamp in ms. Synthetic in tests; only ordering + deltas matter. */
  t: number;
  /** Coordinate-space epoch. Defaults to 0. */
  epoch?: number;
}

export type GestureKind = 'click' | 'drag' | 'pinch';

/** Incremental, tentatively-applicable update. May be rolled back by a
 *  `compensate` output if the gesture is superseded or lost. */
export interface PreviewOutput {
  kind: 'preview';
  gesture: 'drag' | 'pinch';
  gen: number;
  dx: number;
  dy: number;
  scale: number;
  cx: number;
  cy: number;
}

/** Exact inverse of every preview emitted so far for this gesture segment:
 *  sum(preview.dx) + compensate.dx === 0, prod(preview.scale) * compensate.scale === 1. */
export interface CompensateOutput {
  kind: 'compensate';
  gesture: 'drag' | 'pinch';
  gen: number;
  dx: number;
  dy: number;
  scale: number;
}

/** Final, mutually exclusive outcome of a generation. At most one per gen. */
export interface CommitOutput {
  kind: 'commit';
  gesture: GestureKind;
  gen: number;
  dx: number;
  dy: number;
  scale: number;
  duration: number;
  targetId?: string;
}

/** A candidate/active gesture was abandoned without committing. */
export interface CancelOutput {
  kind: 'cancel';
  gesture: GestureKind;
  gen: number;
  reason: string;
}

/** Input that arrived too late, out of order, or for an untracked pointer. */
export interface IgnoredOutput {
  kind: 'ignored';
  reason: string;
  inputType: PointerEventType;
  pointerId: number;
}

export type GestureOutput =
  | PreviewOutput
  | CompensateOutput
  | CommitOutput
  | CancelOutput
  | IgnoredOutput;
