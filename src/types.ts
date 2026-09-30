// Core type definitions. Erasable-syntax-only TypeScript (Node >= 23 type stripping).

export type PointerEventType =
  | 'pointerdown'
  | 'pointermove'
  | 'pointerup'
  | 'pointercancel'
  | 'lostpointercapture';

export interface PointerEventLike {
  /** Monotonic logical sequence number assigned by the trace source. */
  seq: number;
  /** Logical time in ms. Not wall-clock; tests drive it explicitly. */
  time: number;
  pointerId: number;
  type: PointerEventType;
  x: number;
  y: number;
  /** Coordinate space identifier. Events from different spaces interleave freely. */
  space: string;
}

export type GestureKind = 'click' | 'drag' | 'pinch';

export type GesturePhase =
  | 'preview'      // tentative, may be compensated later
  | 'compensate'   // undoes a previously emitted preview
  | 'commit'       // final, mutually exclusive within an interaction
  | 'cancel';      // gesture died before any preview (or after compensate)

export interface GestureEvent {
  kind: GestureKind;
  phase: GesturePhase;
  /** Interaction id: first pointerdown .. last pointer removal. */
  interactionId: number;
  /** Gesture generation: bumps whenever the active pointer set changes. */
  generation: number;
  pointerIds: number[];
  /** Hit-tested target from the fixture node tree (null if miss). */
  targetId: string | null;
  /** Centroid in the canonical space. */
  x: number;
  y: number;
  /** Total translation since gesture anchor. */
  dx: number;
  dy: number;
  /** Pinch scale relative to gesture anchor (1 for non-pinch). */
  scale: number;
  time: number;
}

export interface Thresholds {
  /** Max centroid drift for a click to remain a click (px). */
  clickSlop: number;
  /** Centroid distance from anchor before drag preview starts (px). */
  drag: number;
  /** Relative scale change before pinch preview starts. */
  pinchScale: number;
  /** Pointer-pair centroid drift before pinch preview starts (px). */
  pinchPan: number;
  /** A pointer with no events for this long is expired by tick(). */
  staleTimeout: number;
}

export const DEFAULT_THRESHOLDS: Thresholds = {
  clickSlop: 8,
  drag: 8,
  pinchScale: 0.1,
  pinchPan: 6,
  staleTimeout: 5000,
};
