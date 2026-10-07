/**
 * 版本化文档：维护一条只增不删的编辑链，支持把任意旧版本上的范围映射到当前
 * 版本，并支持以事务为单位的撤销/重做。
 *
 * 模型要点：
 * - 每次事务（transaction）追加一个链节点，版本号 = 链长，单调递增。
 * - 撤销/重做本身也作为新的链节点追加（把被撤销事务的逆编辑重放），因此从
 *   任意旧版本到当前版本的映射始终可以沿链线性折叠，无需特判。
 * - 链节点内的编辑使用“顺序坐标”：第 i 个编辑的坐标基于前 i-1 个编辑应用
 *   后的文档状态，因此可以直接逐个折叠映射。
 */

import {
  RangeAffinity,
  mapPositionThroughEdits,
  mapRangeThroughEdits,
  type Affinity,
  type Edit,
  type Range,
} from './range.ts';

interface ChainEntry {
  /** 正放编辑（顺序坐标），应用后从上一版本得到本版本。 */
  readonly edits: readonly Edit[];
  /** 逆放编辑（顺序坐标），应用后把本版本还原回上一版本。 */
  readonly inverse: readonly Edit[];
}

/** transact 回调里使用的事务构建器；所有坐标都基于事务开始前的文档状态。 */
export interface TransactionBuilder {
  replace(start: number, end: number, text: string): void;
  insert(offset: number, text: string): void;
  delete(start: number, end: number): void;
}

export class VersionedDocument {
  #text: string;
  readonly #chain: ChainEntry[] = [];
  /** 可撤销事务在链中的下标，按时间顺序。 */
  readonly #undoStack: number[] = [];
  /** 可重做事务（即撤销产生的链节点）在链中的下标。 */
  readonly #redoStack: number[] = [];

  constructor(text = '') {
    this.#text = text;
  }

  get text(): string {
    return this.#text;
  }

  get length(): number {
    return this.#text.length;
  }

  /** 当前版本号（= 已追加的链节点数，单调递增，撤销也会产生新版本）。 */
  get version(): number {
    return this.#chain.length;
  }

  get canUndo(): boolean {
    return this.#undoStack.length > 0;
  }

  get canRedo(): boolean {
    return this.#redoStack.length > 0;
  }

  /** 把若干编辑作为一个原子事务应用：一次撤销即可整体回退。返回新版本号。 */
  applyTransaction(edits: readonly Edit[]): number {
    if (edits.length === 0) return this.version;
    const sorted = normalizeEdits(edits, this.#text.length);
    const entry = buildChainEntry(this.#text, sorted);
    this.#text = applySequential(this.#text, entry.edits);
    this.#chain.push(entry);
    this.#undoStack.push(this.#chain.length - 1);
    this.#redoStack.length = 0;
    return this.version;
  }

  /** 便捷方法：单编辑事务。 */
  replace(start: number, end: number, text: string): number {
    return this.applyTransaction([{ start, end, text }]);
  }

  insert(offset: number, text: string): number {
    return this.applyTransaction([{ start: offset, end: offset, text }]);
  }

  delete(start: number, end: number): number {
    return this.applyTransaction([{ start, end, text: '' }]);
  }

  /** 通过回调收集编辑并作为一个事务提交。 */
  transact(build: (tx: TransactionBuilder) => void): number {
    const edits: Edit[] = [];
    build({
      replace: (start, end, text) => {
        edits.push({ start, end, text });
      },
      insert: (offset, text) => {
        edits.push({ start: offset, end: offset, text });
      },
      delete: (start, end) => {
        edits.push({ start, end, text: '' });
      },
    });
    return this.applyTransaction(edits);
  }

  /** 撤销最近一个事务（作为新链节点追加）。无可撤销内容时返回 false。 */
  undo(): boolean {
    return this.#replay(this.#undoStack, this.#redoStack);
  }

  /** 重做最近被撤销的事务。无可重做内容时返回 false。 */
  redo(): boolean {
    return this.#replay(this.#redoStack, this.#undoStack);
  }

  /** 弹出 source 栈顶事务并应用其逆编辑，把新链节点记入 target 栈。 */
  #replay(source: number[], target: number[]): boolean {
    const index = source.pop();
    if (index === undefined) return false;
    const entry = this.#chain[index];
    this.#text = applySequential(this.#text, entry.inverse);
    this.#chain.push({ edits: entry.inverse, inverse: entry.edits });
    target.push(this.#chain.length - 1);
    return true;
  }

  /**
   * 把 fromVersion 版本上的范围沿编辑链映射到当前版本。
   * 编辑触及范围内部时返回 undefined；边界插入按 affinity 映射。
   */
  mapRange(
    target: Range,
    fromVersion: number,
    affinity: RangeAffinity = RangeAffinity.Expand,
  ): Range | undefined {
    this.#assertVersion(fromVersion);
    assertRange(target);
    let current: Range | undefined = target;
    for (let version = fromVersion; version < this.version && current; version++) {
      current = mapRangeThroughEdits(current, this.#chain[version].edits, affinity);
    }
    return current;
  }

  /** 把 fromVersion 版本上的位置映射到当前版本。 */
  mapPosition(
    position: number,
    fromVersion: number,
    affinity: Affinity = 'before',
  ): number | undefined {
    this.#assertVersion(fromVersion);
    if (!Number.isInteger(position) || position < 0) {
      throw new RangeError(`非法位置: ${position}`);
    }
    let current: number | undefined = position;
    for (let version = fromVersion; version < this.version && current !== undefined; version++) {
      current = mapPositionThroughEdits(current, this.#chain[version].edits, affinity);
    }
    return current;
  }

  #assertVersion(version: number): void {
    if (!Number.isInteger(version) || version < 0 || version > this.version) {
      throw new RangeError(`版本 ${version} 不在 [0, ${this.version}] 内`);
    }
  }
}

function assertRange(target: Range): void {
  if (
    !Number.isInteger(target.start) ||
    !Number.isInteger(target.end) ||
    target.start < 0 ||
    target.end < target.start
  ) {
    throw new RangeError(`非法范围: [${target.start}, ${target.end})`);
  }
}

/** 校验并按 start 升序排序编辑；同一事务内的编辑不得重叠。 */
function normalizeEdits(edits: readonly Edit[], textLength: number): Edit[] {
  const sorted = [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
  for (let i = 0; i < sorted.length; i++) {
    const edit = sorted[i];
    if (
      !Number.isInteger(edit.start) ||
      !Number.isInteger(edit.end) ||
      edit.start < 0 ||
      edit.end < edit.start ||
      edit.end > textLength
    ) {
      throw new RangeError(
        `非法编辑: [${edit.start}, ${edit.end})，文本长度 ${textLength}`,
      );
    }
    if (i > 0 && sorted[i - 1].end > edit.start) {
      throw new Error(
        `事务内编辑重叠: [${sorted[i - 1].start}, ${sorted[i - 1].end}) 与 [${edit.start}, ${edit.end})`,
      );
    }
  }
  return sorted;
}

/**
 * 把升序、不重叠、基于同一基准状态的编辑转换为顺序坐标，并同时算出逆编辑。
 */
function buildChainEntry(baseText: string, sorted: readonly Edit[]): ChainEntry {
  const forward: Edit[] = [];
  const inverseReversed: Edit[] = [];
  let text = baseText;
  let delta = 0;
  for (const edit of sorted) {
    const sequential: Edit = {
      start: edit.start + delta,
      end: edit.end + delta,
      text: edit.text,
    };
    const removed = text.slice(sequential.start, sequential.end);
    text =
      text.slice(0, sequential.start) + edit.text + text.slice(sequential.end);
    forward.push(sequential);
    inverseReversed.push({
      start: sequential.start,
      end: sequential.start + edit.text.length,
      text: removed,
    });
    delta += edit.text.length - (edit.end - edit.start);
  }
  // 逆编辑按相反顺序应用，即得顺序坐标的逆链。
  return { edits: forward, inverse: inverseReversed.reverse() };
}

/** 依次应用一串顺序坐标的编辑。 */
function applySequential(text: string, edits: readonly Edit[]): string {
  let result = text;
  for (const edit of edits) {
    result = result.slice(0, edit.start) + edit.text + result.slice(edit.end);
  }
  return result;
}
