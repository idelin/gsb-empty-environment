// Shared types for the truncation engine.
// Erasable-syntax-only TypeScript (runs on Node's built-in type stripping).

export type Dir = 'ltr' | 'rtl';

// ---------- Input node tree (given by fixtures, not a DOM) ----------

export interface TextNode {
  kind: 'text';
  text: string;
  dir?: Dir;
  key?: boolean;       // key fragment: prefer keeping it when truncating
  required?: boolean;  // required key fragment: conflict is reported if dropped
  keyId?: string;
}

export interface EntityNode {
  kind: 'entity';
  id: string;
  text: string;        // unbreakable inline entity (chip, mention, ...)
  dir?: Dir;
  key?: boolean;
  required?: boolean;
  keyId?: string;
}

export interface LinkNode {
  kind: 'link';
  id: string;
  href: string;
  children: InlineNode[];
}

export type InlineNode = TextNode | EntityNode | LinkNode;

export interface BlockInput {
  children: InlineNode[];
  dir?: Dir; // explicit paragraph direction; otherwise first-strong wins
}

// ---------- Glyph metrics (fixture-given, versioned) ----------

export interface MetricTable {
  version: number;
  defaultWidth: number;              // per-cluster fallback width
  ellipsisWidth?: number;            // defaults to measuring "…"
  spaceWidth?: number;               // defaults to measuring " "
  clusters: Record<string, number>;  // exact width per grapheme cluster
}

// ---------- Flattened cluster stream ----------

export interface ClusterItem {
  kind: 'cluster' | 'ellipsis';
  text: string;
  srcStart: number;   // offset into the concatenated source string (-1 for ellipsis)
  srcEnd: number;
  dir: Dir;
  linkId?: string;
  href?: string;
  key: boolean;
  required: boolean;
  keyId?: string;
  atomicId: number;   // 0 = free; >0 = same unbreakable group
  breakAfter: boolean;
  width: number;      // filled during measurement
}

// ---------- Layout result ----------

export type TruncationMode = 'end' | 'middle' | 'key';

export interface LayoutOptions {
  width: number;
  maxLines: number;
  mode: TruncationMode;
}

export interface VisualItem {
  kind: 'cluster' | 'ellipsis';
  text: string;
  x: number;
  width: number;
  line: number;
  dir: Dir;
  srcStart: number;  // -1 for ellipsis
  srcEnd: number;
  linkId?: string;
  href?: string;
  key: boolean;
  keyId?: string;
}

export interface Rect { line: number; x: number; width: number }

export interface LinkFragment {
  linkId: string;
  href: string;
  // visible pieces of the same original link range, in source order
  ranges: Array<{ start: number; end: number }>;
  rects: Rect[];
}

export interface ConflictInfo {
  type: 'key-overflow';
  keyIds: string[];
  requiredWidth: number;
  capacity: number;
  message: string;
}

export interface DegradedInfo {
  reason: 'ellipsis-too-wide';
  ellipsisWidth: number;
  lineWidth: number;
  message: string;
}

export interface LayoutResult {
  paraDir: Dir;
  lines: VisualItem[][];   // visual order per line, x grows left->right
  truncated: boolean;
  degraded?: DegradedInfo;
  conflicts: ConflictInfo[];
  linkFragments: LinkFragment[];
  metricVersion: number;
  source: string;          // full original text (accessible name)
  ellipsis?: { line: number; x: number; width: number };
}
