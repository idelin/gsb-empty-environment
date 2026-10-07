/**
 * 范围映射核心：把基于旧文档版本的范围（Range）穿过编辑（Edit）映射到新版本。
 *
 * 坐标约定：所有位置均为 0 起始的 UTF-16 偏移量，范围为半开区间 [start, end)。
 */

/** 半开区间 [start, end)，start <= end。 */
export interface Range {
  readonly start: number;
  readonly end: number;
}

/** 一次编辑：把 [start, end) 的内容替换为 text（start === end 时为纯插入）。 */
export interface Edit {
  readonly start: number;
  readonly end: number;
  readonly text: string;
}

/**
 * 位置亲和性：当文本恰好插入在某个被跟踪位置上时，该位置的去向。
 * - 'before'：位置停留在插入文本之前（插入物被排除在位置之后）。
 * - 'after'：位置移动到插入文本之后。
 */
export type Affinity = 'before' | 'after';

/** 范围两个端点各自的亲和性。 */
export interface RangeAffinity {
  readonly start: Affinity;
  readonly end: Affinity;
}

/** 常用亲和性预设。 */
export const RangeAffinity = {
  /** 边界插入被范围吸收（范围扩张）。适合补全的替换范围：用户在边界继续输入时范围跟随扩大。 */
  Expand: Object.freeze({ start: 'before', end: 'after' } as RangeAffinity),
  /** 边界插入被排除在范围之外（范围保持收缩）。 */
  Collapse: Object.freeze({ start: 'after', end: 'before' } as RangeAffinity),
} as const;

export function range(start: number, end: number): Range {
  return { start, end };
}

export function isValidRange(value: Range): boolean {
  return (
    Number.isInteger(value.start) &&
    Number.isInteger(value.end) &&
    value.start >= 0 &&
    value.end >= value.start
  );
}

export function rangesEqual(a: Range, b: Range): boolean {
  return a.start === b.start && a.end === b.end;
}

/**
 * 把单个位置映射穿过一次编辑。
 *
 * - 编辑之前的文本：位置不变。
 * - 编辑之后的文本：位置平移。
 * - 纯插入恰好落在该位置上：按 affinity 决定停留还是随插入移动。
 * - 位置落在被删除/替换内容的严格内部：返回 undefined（无法确定去向）。
 * - 位置落在非插入编辑的边界上：贴到替换后内容的对应边缘。
 */
export function mapPositionThroughEdit(
  position: number,
  edit: Edit,
  affinity: Affinity,
): number | undefined {
  const delta = edit.text.length - (edit.end - edit.start);
  if (position < edit.start) return position;
  if (position > edit.end) return position + delta;
  if (edit.start === edit.end) {
    // 纯插入恰好落在该位置。
    return affinity === 'after' ? position + edit.text.length : position;
  }
  // position 落在被替换区间 [edit.start, edit.end] 上（非插入）。
  if (position === edit.start) return edit.start;
  if (position === edit.end) return edit.start + edit.text.length;
  return undefined; // 严格内部：被删除/覆盖，位置失效。
}

/**
 * 把范围映射穿过一次编辑。
 *
 * 失效规则：只要编辑所替换的区间与范围的严格内部相交（即删改动了范围内部的
 * 内容），整个范围失效，返回 undefined。范围内部的纯插入不算“触及”，范围会
 * 自然扩张以包含插入内容；边界上的纯插入按各端点的 affinity 映射。
 *
 * 映射后若两端点交叉（仅可能发生在空范围遇到恰在其上的插入时），范围坍缩为
 * 映射后起点处的空范围。
 */
export function mapRangeThroughEdit(
  target: Range,
  edit: Edit,
  affinity: RangeAffinity,
): Range | undefined {
  // 编辑删除了范围严格内部的内容 → 失效。
  if (
    edit.start !== edit.end &&
    Math.max(edit.start, target.start) < Math.min(edit.end, target.end)
  ) {
    return undefined;
  }
  const start = mapPositionThroughEdit(target.start, edit, affinity.start);
  const end = mapPositionThroughEdit(target.end, edit, affinity.end);
  if (start === undefined || end === undefined) return undefined;
  if (start <= end) return { start, end };
  return { start, end: start }; // 端点交叉：坍缩为空范围。
}

/**
 * 依次把范围映射穿过一串顺序编辑。edits 中第 i 个编辑的坐标基于第 i-1 个
 * 编辑应用之后的文档状态。任一步失效则整体返回 undefined。
 */
export function mapRangeThroughEdits(
  target: Range,
  edits: readonly Edit[],
  affinity: RangeAffinity,
): Range | undefined {
  let current: Range | undefined = target;
  for (const edit of edits) {
    if (current === undefined) return undefined;
    current = mapRangeThroughEdit(current, edit, affinity);
  }
  return current;
}

/** 把单个位置依次映射穿过一串顺序编辑。 */
export function mapPositionThroughEdits(
  position: number,
  edits: readonly Edit[],
  affinity: Affinity,
): number | undefined {
  let current: number | undefined = position;
  for (const edit of edits) {
    if (current === undefined) return undefined;
    current = mapPositionThroughEdit(current, edit, affinity);
  }
  return current;
}
