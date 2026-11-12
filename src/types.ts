/**
 * Public type surface for the RPA form-binding package.
 *
 * The host automation layer supplies an accessibility tree (nodes with
 * stable ids, roles, parent links and visible rectangles), the explicit
 * label relations advertised by the UI framework, and tuning weights.
 */

/** Axis-aligned visible rectangle in screen coordinates (pixels). */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * Position of a control relative to its candidate label.
 * `overlap` means the rectangles intersect on both axes.
 */
export type RelativeDirection = 'left' | 'right' | 'above' | 'below' | 'overlap';

/** A single node from the accessibility tree. */
export interface AccessNode {
  /** Stable accessibility id, unique within the tree. */
  id: string;
  /** ARIA / accessibility role, e.g. `textbox`, `checkbox`, `form`. */
  role: string;
  /** Accessible name (visible label text for label nodes). */
  name?: string | null;
  /** Parent node id; null/undefined for roots. */
  parentId?: string | null;
  /** Visible rectangle. Absent means the node is not rendered/visible. */
  rect?: Rect;
  /** Explicitly hidden from the accessibility tree or off-screen. */
  hidden?: boolean;
  /** Marks this node as a label that should be bound to a control. */
  isLabel?: boolean;
  /** Roles this label expects (from e.g. `aria-label` semantics). */
  expectedControlRoles?: string[];
}

/** Explicit label/control relationship reported by the framework. */
export interface ExplicitLabelRelation {
  labelId: string;
  controlId: string;
  /** Origin of the relation, e.g. `for`, `labelledby`, `aria-label-ref`. */
  kind?: string;
}

/** Tunable cost components. Every component is weighted from the input. */
export interface CostWeights {
  /** Multiplier applied to the directional geometric distance. */
  distance: number;
  /** Flat penalty when the control is not in a preferred direction. */
  wrongDirection: number;
  /** Flat penalty when the control role is not an expected role. */
  roleMismatch: number;
}

export interface BindOptions {
  /** Partial weights; missing entries fall back to package defaults. */
  weights?: Partial<CostWeights>;
  /**
   * Directions (control relative to label) considered preferred.
   * Defaults to labels reading with controls to their left or below them:
   * `['left', 'above']` means a control to the right of / below a label is
   * penalized.
   */
  preferredDirections?: RelativeDirection[];
  /** Minimum best-vs-second-best cost margin required to accept. */
  ambiguityThreshold?: number;
  /** Roles that establish a form/dialog ancestry boundary. */
  formRoles?: string[];
  /** When true, hidden controls remain eligible (still evidence flagged). */
  includeHidden?: boolean;
}

export type BindingStatus = 'bound' | 'ambiguous' | 'unbound';
export type BindingSource = 'explicit' | 'implicit';

/** Full cost breakdown for one label/control candidate edge. */
export interface EdgeCost {
  labelId: string;
  controlId: string;
  /** Raw geometric distance before weighting. */
  rawDistance: number;
  direction: RelativeDirection;
  preferredDirection: boolean;
  roleMismatch: boolean;
  /** Final weighted cost. Infinity when the edge is forbidden. */
  cost: number;
  /** Why this edge is forbidden or penalized. */
  reasons: string[];
  forbidden: boolean;
}

/** Per-field binding decision with auditable evidence. */
export interface FieldBinding {
  labelId: string;
  labelName: string | null;
  status: BindingStatus;
  /** `explicit` for framework relations, `implicit` for cost matching. */
  source?: BindingSource;
  controlId?: string;
  controlRole?: string;
  /** Cost of the chosen edge. */
  cost?: number;
  /** Cost of the best assignment that does not use the chosen edge. */
  secondBestCost?: number;
  /** `secondBestCost - cost`; small margins mean ambiguity. */
  margin?: number;
  explicitKind?: string;
  /** Chosen-edge evidence (implicit) or corroborating geometry (explicit). */
  edge?: EdgeCost;
  /** Allowed candidate edges sorted by ascending cost. */
  candidates: EdgeCost[];
  /** Human-readable audit trail. */
  reasons: string[];
}

export interface BindingResult {
  /**
   * True only when every label resolved unambiguously and the global
   * best-vs-second-best assignment margin clears the threshold.
   */
  accepted: boolean;
  bestCost: number | null;
  secondBestCost: number | null;
  /** Global margin between the best and the next-best full assignment. */
  globalMargin: number | null;
  threshold: number;
  fields: FieldBinding[];
  /** Non-fatal problems encountered while building the result. */
  diagnostics: string[];
}

export const DEFAULT_WEIGHTS: CostWeights = {
  distance: 1,
  wrongDirection: 100,
  roleMismatch: 50,
};

/**
 * UI convention: the control sits to the right of its label or on the
 * next row below it. Callers can override via BindOptions.
 */
export const DEFAULT_PREFERRED_DIRECTIONS: RelativeDirection[] = ['right', 'below'];
export const DEFAULT_FORM_ROLES = ['form', 'dialog'];
export const DEFAULT_AMBIGUITY_THRESHOLD = 1;
