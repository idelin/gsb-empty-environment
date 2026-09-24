/**
 * 随编辑移动的批注区间引擎。
 *
 * - 所有公开偏移均为 UTF-16 code unit 偏移（与 JS 字符串下标一致）。
 * - 编辑与批注边界永远不会切开 UTF-16 代理对（emoji 等增补平面字符）。
 * - 每个端点可独立选择粘附方向：
 *     inward  —— 端点处的插入不纳入批注；
 *     outward —— 端点处的插入纳入批注。
 * - 批注覆盖的内容被全部删除时，批注折叠为零宽锚点保留在原位。
 * - 批量编辑按 (start, 操作ID) 排序后顺序应用；同位置插入最终文本顺序与操作ID一致。
 * - undo() 恢复编辑前的文本与全部锚点。
 */

export type Affinity = 'inward' | 'outward';

export interface Annotation {
  id: number;
  /** UTF-16 偏移，半开区间 [start, end) */
  start: number;
  end: number;
  startAffinity: Affinity;
  endAffinity: Affinity;
}

export interface Edit {
  /** 操作ID：批量编辑中同位置操作按此升序应用 */
  id: number;
  /** 被替换区间 [start, end)，UTF-16 偏移；start === end 表示纯插入 */
  start: number;
  end: number;
  /** 替换文本；空串表示纯删除 */
  text: string;
}

interface Snapshot {
  text: string;
  annotations: Annotation[];
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** offset 是否落在代理对中间（前为高代理、后为低代理） */
function splitsPair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

/** 区间起点/插入点：若切开代理对则左移，把整对留在右侧 */
function snapStart(text: string, offset: number): number {
  return splitsPair(text, offset) ? offset - 1 : offset;
}

/** 区间终点：若切开代理对则右移，把整对留在左侧 */
function snapEnd(text: string, offset: number): number {
  return splitsPair(text, offset) ? offset + 1 : offset;
}

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(Math.max(value, lo), hi);
}

/**
 * 把批注端点位置 p 映射到编辑应用后的新位置。
 * 编辑：将 [s, e) 替换为长度 insertedLen 的文本。
 */
function mapPosition(
  p: number,
  isRangeStart: boolean,
  affinity: Affinity,
  s: number,
  e: number,
  insertedLen: number,
): number {
  const delta = insertedLen - (e - s);
  if (p < s) return p;
  if (p > e) return p + delta;
  if (s === e) {
    // 纯插入恰好落在端点上：由端点粘附方向决定是否纳入
    if (isRangeStart) return affinity === 'inward' ? p + insertedLen : p;
    return affinity === 'inward' ? p : p + insertedLen;
  }
  if (p === s) return s; // 删除起点贴着端点：端点留在塌陷处
  if (p === e) return s + insertedLen; // 删除终点贴着端点：端点移到替换文本之后
  return s; // 严格位于被删内容内部：塌陷到删除点
}

/**
 * 把“尚未应用的后续编辑”的坐标映射到当前文档。
 * 同位置插入映射到已插入文本之后，保证最终顺序与操作ID一致。
 */
function mapEditCoord(p: number, s: number, e: number, insertedLen: number): number {
  const delta = insertedLen - (e - s);
  if (p < s) return p;
  if (p > e) return p + delta;
  if (s === e) return p + insertedLen;
  return s + insertedLen; // 落入已删除区域：塌陷到替换文本之后
}

export class AnnotatedDocument {
  #text: string;
  #annotations = new Map<number, Annotation>();
  #nextAnnotationId = 1;
  #history: Snapshot[] = [];

  constructor(text = '') {
    this.#text = text;
  }

  get text(): string {
    return this.#text;
  }

  get length(): number {
    return this.#text.length;
  }

  /** 当前全部批注（按 id 升序的副本） */
  getAnnotations(): Annotation[] {
    return [...this.#annotations.values()]
      .map((a) => ({ ...a }))
      .sort((a, b) => a.id - b.id);
  }

  getAnnotation(id: number): Annotation | undefined {
    const a = this.#annotations.get(id);
    return a ? { ...a } : undefined;
  }

  /** 新建批注；偏移会被夹取并对齐到代理对边界，保证不切开代理对 */
  addAnnotation(
    start: number,
    end: number,
    startAffinity: Affinity = 'inward',
    endAffinity: Affinity = 'inward',
  ): number {
    let s = snapStart(this.#text, clamp(start, 0, this.#text.length));
    let e = snapEnd(this.#text, clamp(end, 0, this.#text.length));
    if (s > e) [s, e] = [e, s];
    const id = this.#nextAnnotationId++;
    this.#annotations.set(id, { id, start: s, end: e, startAffinity, endAffinity });
    return id;
  }

  removeAnnotation(id: number): boolean {
    return this.#annotations.delete(id);
  }

  /** 单条编辑，等价于 applyBatch([edit]) */
  applyEdit(edit: Edit): void {
    this.applyBatch([edit]);
  }

  /**
   * 批量编辑：按 (start, id) 升序排序后顺序应用，
   * 后续编辑的坐标随已应用编辑同步变换，因此全部编辑都以原始文档坐标给出。
   */
  applyBatch(edits: Edit[]): void {
    if (edits.length === 0) return;
    this.#history.push({
      text: this.#text,
      annotations: this.getAnnotations(),
    });

    const sorted = edits
      .map((edit) => this.#normalizeEdit(edit))
      .sort((a, b) => a.start - b.start || a.id - b.id);

    const applied: Edit[] = [];
    for (const edit of sorted) {
      // 把该编辑的原始坐标映射到当前文档坐标
      let s = edit.start;
      let e = edit.end;
      for (const prev of applied) {
        const inserted = prev.text.length;
        s = mapEditCoord(s, prev.start, prev.end, inserted);
        e = mapEditCoord(e, prev.start, prev.end, inserted);
      }
      if (e < s) e = s; // 覆盖删除不产生负长度
      s = clamp(s, 0, this.#text.length);
      e = clamp(e, s, this.#text.length);

      this.#text = this.#text.slice(0, s) + edit.text + this.#text.slice(e);
      this.#transformAnnotations(s, e, edit.text.length);
      applied.push({ ...edit, start: s, end: e });
    }
  }

  /** 撤销最近一次 applyEdit / applyBatch，恢复文本与全部锚点 */
  undo(): boolean {
    const snapshot = this.#history.pop();
    if (!snapshot) return false;
    this.#text = snapshot.text;
    this.#annotations = new Map(snapshot.annotations.map((a) => [a.id, { ...a }]));
    return true;
  }

  #normalizeEdit(edit: Edit): Edit {
    let s = clamp(edit.start, 0, this.#text.length);
    let e = clamp(edit.end, 0, this.#text.length);
    if (e < s) e = s;
    if (s === e) {
      s = snapStart(this.#text, s); // 插入点不切开代理对：移到代理对之前
      e = s;
    } else {
      s = snapStart(this.#text, s); // 删除/替换把代理对作为整体
      e = snapEnd(this.#text, e);
    }
    return { ...edit, start: s, end: e };
  }

  #transformAnnotations(s: number, e: number, insertedLen: number): void {
    for (const ann of this.#annotations.values()) {
      let start = mapPosition(ann.start, true, ann.startAffinity, s, e, insertedLen);
      const end = mapPosition(ann.end, false, ann.endAffinity, s, e, insertedLen);
      if (start > end) start = end; // 零宽锚点两端向内时保持一致，不产生负长度
      ann.start = start;
      ann.end = end;
    }
  }
}
