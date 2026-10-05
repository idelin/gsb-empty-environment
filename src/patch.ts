/**
 * 版本补丁：基于 OT 文本操作（retain / insert / delete）的文档变换。
 *
 * 一个 Patch 按顺序消费旧文档并产出新文档，所有 op 拼接后必须恰好覆盖整个旧文档。
 * 同一断点处的规范顺序为“插入在删除之前”（由 normalize 保证），
 * 这样 transformPosition 的组合律才成立：
 *   transformPosition(p, compose(a, b)) === transformPosition(transformPosition(p, a), b)
 */

export type Assoc = 'left' | 'right';

export type Op =
  | { type: 'retain'; count: number }
  | { type: 'insert'; text: string }
  | { type: 'delete'; count: number };

export type Patch = Op[];

/** 用户友好的编辑形式：把旧文档 [start, end) 替换为 text。 */
export interface Edit {
  start: number;
  end: number;
  text: string;
}

function opLength(op: Op): number {
  return op.type === 'insert' ? op.text.length : op.count;
}

/**
 * 规范化补丁：
 * 1. 丢弃零长度 op，合并相邻同类 op；
 * 2. 把同一断点处的 [delete, insert] 交换为 [insert, delete]（插入优先），
 *    交换不改变 apply 结果，但能让位置映射在组合下保持一致。
 */
export function normalize(patch: Patch): Patch {
  const merged: Op[] = [];
  for (const op of patch) {
    if (op.type === 'insert' && op.text.length === 0) continue;
    if (op.type !== 'insert' && op.count === 0) continue;
    const last = merged[merged.length - 1];
    if (last && last.type === 'retain' && op.type === 'retain') {
      merged[merged.length - 1] = { type: 'retain', count: last.count + op.count };
      continue;
    }
    if (last && last.type === 'delete' && op.type === 'delete') {
      merged[merged.length - 1] = { type: 'delete', count: last.count + op.count };
      continue;
    }
    if (last && last.type === 'insert' && op.type === 'insert') {
      merged[merged.length - 1] = { type: 'insert', text: last.text + op.text };
      continue;
    }
    merged.push(op);
  }
  const out: Op[] = [];
  for (const op of merged) {
    const last = out[out.length - 1];
    if (op.type === 'insert' && last && last.type === 'delete') {
      out[out.length - 1] = op;
      out.push(last);
    } else {
      out.push(op);
    }
  }
  return out;
}

/** 把补丁应用到文档上，返回新文档。补丁必须恰好覆盖整个文档。 */
export function apply(doc: string, patch: Patch): string {
  let oldPos = 0;
  let out = '';
  for (const op of patch) {
    if (op.type === 'retain') {
      if (oldPos + op.count > doc.length) {
        throw new RangeError('apply: retain exceeds document length');
      }
      out += doc.slice(oldPos, oldPos + op.count);
      oldPos += op.count;
    } else if (op.type === 'insert') {
      out += op.text;
    } else {
      if (oldPos + op.count > doc.length) {
        throw new RangeError('apply: delete exceeds document length');
      }
      oldPos += op.count;
    }
  }
  if (oldPos !== doc.length) {
    throw new RangeError('apply: patch does not cover the whole document');
  }
  return out;
}

/**
 * 由编辑列表构造补丁。编辑可以乱序给出，但同一条补丁内不允许重叠；
 * 连续补丁之间的重叠替换由 compose 处理。
 */
export function fromEdits(docLength: number, edits: Edit[]): Patch {
  const sorted = edits.slice().sort((x, y) => x.start - y.start || x.end - y.end);
  const ops: Op[] = [];
  let pos = 0;
  for (const edit of sorted) {
    if (edit.start < 0 || edit.end < edit.start || edit.end > docLength) {
      throw new RangeError('fromEdits: edit out of range: ' + JSON.stringify(edit));
    }
    if (edit.start < pos) {
      throw new Error('fromEdits: overlapping edits within one patch: ' + JSON.stringify(edit));
    }
    if (edit.start > pos) ops.push({ type: 'retain', count: edit.start - pos });
    if (edit.text.length > 0) ops.push({ type: 'insert', text: edit.text });
    if (edit.end > edit.start) ops.push({ type: 'delete', count: edit.end - edit.start });
    pos = edit.end;
  }
  if (pos < docLength) ops.push({ type: 'retain', count: docLength - pos });
  return normalize(ops);
}

/** 把补丁还原为编辑列表（主要用于调试与断言）。 */
export function toEdits(patch: Patch): Edit[] {
  const edits: Edit[] = [];
  let oldPos = 0;
  let pending: Edit | null = null;
  for (const op of patch) {
    if (op.type === 'retain') {
      if (pending) {
        edits.push(pending);
        pending = null;
      }
      oldPos += op.count;
    } else if (op.type === 'insert') {
      if (!pending) pending = { start: oldPos, end: oldPos, text: '' };
      pending.text += op.text;
    } else {
      if (!pending) pending = { start: oldPos, end: oldPos, text: '' };
      oldPos += op.count;
      pending.end = oldPos;
    }
  }
  if (pending) edits.push(pending);
  return edits;
}

/**
 * 组合两个连续补丁：compose(a, b) 满足
 *   apply(doc, compose(a, b)) === apply(apply(doc, a), b)
 * 连续补丁之间的替换允许重叠（后一个补丁可以改、删前一个补丁刚插入的文本）。
 */
export function compose(first: Patch, second: Patch): Patch {
  const a = normalize(first);
  const b = normalize(second);
  const out: Op[] = [];
  let i = 0;
  let j = 0;
  let offA = 0;
  let offB = 0;

  while (i < a.length || j < b.length) {
    const opB = j < b.length ? b[j] : undefined;
    // 同一断点处先放行 b 的插入，保证组合结果中“插入在删除之前”。
    if (opB && opB.type === 'insert') {
      out.push(opB);
      j++;
      continue;
    }
    const opA = i < a.length ? a[i] : undefined;
    if (opA && opA.type === 'delete') {
      out.push(opA);
      i++;
      continue;
    }
    if (!opA || !opB) {
      throw new Error('compose: patch length mismatch');
    }
    // 此时 opA 为 retain|insert，opB 为 retain|delete，按公共长度合并。
    const n = Math.min(opLength(opA) - offA, opLength(opB) - offB);
    if (opA.type === 'retain' && opB.type === 'retain') {
      out.push({ type: 'retain', count: n });
    } else if (opA.type === 'retain' && opB.type === 'delete') {
      out.push({ type: 'delete', count: n });
    } else if (opA.type === 'insert' && opB.type === 'retain') {
      out.push({ type: 'insert', text: opA.text.slice(offA, offA + n) });
    }
    // insert + delete 相互抵消，不产生任何 op。
    offA += n;
    offB += n;
    if (offA === opLength(opA)) {
      i++;
      offA = 0;
    }
    if (offB === opLength(opB)) {
      j++;
      offB = 0;
    }
  }
  return normalize(out);
}

/** 求逆补丁：apply(apply(doc, patch), invert(doc, patch)) === doc。 */
export function invert(oldDoc: string, patch: Patch): Patch {
  const out: Op[] = [];
  let oldPos = 0;
  for (const op of patch) {
    if (op.type === 'retain') {
      out.push(op);
      oldPos += op.count;
    } else if (op.type === 'insert') {
      out.push({ type: 'delete', count: op.text.length });
    } else {
      out.push({ type: 'insert', text: oldDoc.slice(oldPos, oldPos + op.count) });
      oldPos += op.count;
    }
  }
  return normalize(out);
}

/**
 * 把旧文档中的光标位置 pos（字符之间的 caret，0..doc.length）映射到新文档。
 * 返回 null 表示 pos 被删除覆盖。
 *
 * 语义（保证组合律成立的关键）：caret 依附于 assoc 指向的相邻字符——
 * 'left' 依附 pos 左侧字符，'right' 依附 pos 右侧字符。
 * 被依附的字符被删除时返回 null；落在插入点时，'left' 停在插入文本之前，
 * 'right' 跟到插入文本之后（仍依附原右侧字符，后续删除覆盖它时同样返回 null）。
 */
export function transformPosition(pos: number, patch: Patch, assoc: Assoc): number | null {
  if (pos < 0) throw new RangeError('transformPosition: pos must be >= 0');
  let oldPos = 0;
  let newPos = 0;
  for (const op of patch) {
    if (op.type === 'retain') {
      if (pos < oldPos + op.count) return newPos + (pos - oldPos);
      oldPos += op.count;
      newPos += op.count;
    } else if (op.type === 'insert') {
      if (pos === oldPos && assoc === 'left') return newPos;
      // right：先越过插入文本，但继续扫描——后续 delete 可能覆盖所依附的字符。
      newPos += op.text.length;
    } else {
      const start = oldPos;
      const end = oldPos + op.count;
      if (assoc === 'left') {
        // left：依附 pos 左侧字符，[start, end) 覆盖它当且仅当 start < pos <= end。
        if (pos > start && pos <= end) return null;
        if (pos === start) return newPos;
        oldPos += op.count;
      } else {
        // right：依附 pos 右侧字符，被覆盖当且仅当 start <= pos < end。
        // pos === end 时不提前返回：右侧字符可能被后续 delete 覆盖。
        if (pos >= start && pos < end) return null;
        oldPos += op.count;
      }
    }
  }
  if (pos > oldPos) {
    throw new RangeError('transformPosition: pos beyond document end');
  }
  return newPos;
}
