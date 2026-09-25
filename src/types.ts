/**
 * Core type model.
 *
 * Directional levels are GIVEN by the caller (fixture / upstream BiDi stage);
 * this module never performs full Unicode bidi classification itself.
 * Level parity follows UAX #9: even => left-to-right, odd => right-to-left.
 */

export type Level = number;
export type Direction = 'ltr' | 'rtl';

export function dirOf(level: Level): Direction {
  return (level & 1) === 0 ? 'ltr' : 'rtl';
}

/**
 * A leaf run in the node tree. `level`, when present, applies to every
 * cluster in the run; otherwise the level is inherited from an enclosing
 * span. Runs may be split/merged freely - offsets are always cluster based.
 */
export interface TextNode {
  /** unique id, used for scoped level updates */
  id: string;
  text: string;
  level?: Level;
}

export interface SpanNode {
  id: string;
  /** level inherited by descendant text nodes that do not declare one */
  level?: Level;
  children: TreeNode[];
}

export type TreeNode = TextNode | SpanNode;

export function isTextNode(node: TreeNode): node is TextNode {
  return typeof (node as TextNode).text === 'string';
}

/** Grapheme metrics supplied by the fixture - no browser layout engine. */
export interface Metrics {
  /** width of one grapheme cluster, in arbitrary visual units */
  widthOf(cluster: string, nodeId: string): number;
}

/** A cursor-atomic unit: a grapheme cluster, with its source location. */
export interface Cluster {
  /** cluster index => also the logical offset of its leading boundary */
  index: number;
  text: string;
  level: Level;
  dir: Direction;
  nodeId: string;
  /** offset in UTF-16 code units, provided as a secondary coordinate */
  codeUnitOffset: number;
  width: number;
  /** hard paragraph break */
  hardBreak: boolean;
}

/**
 * A box is a maximal run of same-level clusters on one visual line
 * (a line may split a run, so box !== source run).
 */
export interface Box {
  clusterIndex: number;
  text: string;
  level: Level;
  dir: Direction;
  width: number;
  x: number;
}

/**
 * Which physical edge of a cluster an affinity attaches to.
 *  - 'leading'  = the edge where reading the cluster starts
 *  - 'trailing' = the edge where reading the cluster ends
 * For ltr leading == left edge; for rtl it is mirrored.
 * `level` records the level that justifies the bias.
 */
export interface Affinity {
  edge: 'leading' | 'trailing';
  level: Level;
}

/**
 * At a soft wrap the same logical offset has a home on both visual lines.
 * 'upper' sits at the end of the upper line, 'lower' at the start of the
 * lower line.
 */
export type LineBias = 'upper' | 'lower';

export interface Caret {
  version: number;
  line: number;
  offset: number;
  x: number;
  affinity: Affinity;
  lineBias: LineBias;
}

export interface VisualLine {
  index: number;
  /** boxes of the line in VISUAL (left-to-right) order */
  boxes: Box[];
  /** first cluster index (logical) on the line */
  startOffset: number;
  /** one past the last cluster index on the line */
  endOffset: number;
  width: number;
  /** geometry for hit testing / vertical motion; units match cluster widths */
  y: number;
  height: number;
}

export interface LayoutSnapshot {
  version: number;
  clusters: Cluster[];
  lines: VisualLine[];
  /** all caret stops ordered by visual reading order across all lines */
  carets: Caret[];
  /** clusters per line, for O(1) position math */
  lineRanges: Array<{ start: number; end: number }>;
}

/** Thrown whenever a caret/selection from an older layout touches a new one. */
export class StaleLayoutError extends Error {
  readonly expected: number;
  readonly actual: number;

  constructor(expected: number, actual: number) {
    super(`stale layout handle: object belongs to layout v${expected}, current is v${actual}`);
    this.name = 'StaleLayoutError';
    this.expected = expected;
    this.actual = actual;
  }
}
