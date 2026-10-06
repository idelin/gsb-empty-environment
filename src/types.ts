export interface Point {
  x: number;
  y: number;
}

/** Axis-aligned rectangle: covers [x, x+width] x [y, y+height]. */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Canvas is the rectangle [0, width] x [0, height]. */
export interface Canvas {
  width: number;
  height: number;
}

export interface Candidate {
  rect: Rect;
}

export interface LabelSpec {
  id: string;
  /** Fixed anchor point the label belongs to. */
  anchor: Point;
  /** Reward for placing this label. Counted only when actually placed. */
  weight: number;
  /** Up to 4 candidate placements; at most one is chosen. */
  candidates: Candidate[];
  /**
   * Hidden labels are excluded from placement entirely: they are never
   * selected, their weight never counts, and they never block others.
   */
  hidden?: boolean;
}

export interface Placement {
  labelId: string;
  candidateIndex: number;
  rect: Rect;
  weight: number;
  /** Distance from the anchor to the nearest point of the placed rect. */
  displacement: number;
}

export interface ConflictPair {
  labelA: string;
  labelB: string;
  /** Number of (candidateA, candidateB) pairs that strictly overlap. */
  overlappingCandidates: number;
}

export interface SolveResult {
  placements: Placement[];
  totalWeight: number;
  totalDisplacement: number;
  /** Pairs of visible labels whose candidate sets compete for space. */
  conflicts: ConflictPair[];
  /** Visible labels that could not be placed in the optimal solution. */
  unplaced: string[];
  /** Hidden labels, reported for transparency; never placed. */
  hidden: string[];
}
