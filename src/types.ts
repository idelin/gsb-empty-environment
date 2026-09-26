// ---- 输入:节点树(夹具给出,不依赖浏览器 DOM) ----

export type Direction = 'ltr' | 'rtl';

export type InlineNode =
  | { kind: 'text'; text: string; dir?: Direction }
  | { kind: 'link'; linkId: string; children: InlineNode[] }
  | { kind: 'entity'; entityId: string; text: string };

// ---- 字形度量(夹具给出,可晚到、可换版本) ----

export interface GlyphMetricTable {
  /** 单调递增的版本号;晚到的度量必须携带更新的版本才会被接受 */
  version: number;
  /** 缺省簇宽(未命中 cluster 表时按码位数 * fallback) */
  fallbackAdvance: number;
  /** 簇 -> 前进宽度 */
  clusters: Record<string, number>;
}

// ---- 展平后的原子:不可再分的最小布局单位 ----

export type AtomKind = 'cluster' | 'entity';

export interface Atom {
  kind: AtomKind;
  /** 展示文本(簇本身 / 实体文本) */
  text: string;
  /** 原文(逻辑序)区间 [start, end) */
  start: number;
  end: number;
  dir: Direction;
  /** 所属链接 id;同一链接被省略区切开后各片段仍指向同一原范围 */
  linkId: string | null;
  /** 链接在原文中的完整范围 */
  linkRange: { start: number; end: number } | null;
  /** 实体不可拆 */
  entityId: string | null;
  /** 之后是否允许断行(空格之后) */
  breakAfter: boolean;
}

// ---- 布局输出 ----

export interface EllipsisToken {
  kind: 'ellipsis';
  /** 被省略的原子区间(逻辑序 atom 下标) */
  covers: { atomStart: number; atomEnd: number };
}

export type VisualItem =
  | { kind: 'atom'; atom: Atom; x: number; width: number }
  | { kind: 'ellipsis'; x: number; width: number; covers: { atomStart: number; atomEnd: number } };

export interface LayoutLine {
  y: number;
  /** 视觉序(渲染顺序)的条目 */
  items: VisualItem[];
  width: number;
}

export interface KeyFragment {
  id: string;
  /** 必须保留的原文区间 */
  start: number;
  end: number;
}

export interface Conflict {
  type: 'key-fragment-over-capacity';
  /** 互相挤爆容量的关键片段 */
  fragments: { id: string; requiredWidth: number }[];
  availableWidth: number;
  requiredWidth: number;
}

export interface Degradation {
  type: 'ellipsis-too-wide';
  ellipsisWidth: number;
  lineWidth: number;
}

export interface LayoutResult {
  lines: LayoutLine[];
  truncated: boolean;
  /** 显式冲突:关键片段互相超容量 */
  conflicts: Conflict[];
  /** 显式退化:省略号本身超宽 */
  degradations: Degradation[];
  /** 本次布局使用的度量版本 */
  metricsVersion: number;
  /** 布局代次,每次重排 +1,用于防止旧测量覆盖新布局 */
  generation: number;
}
