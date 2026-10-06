/**
 * 格式操作：按字符范围增删标记。
 * - 范围跨节点时先拆分，应用后再规范化合并；
 * - 拆分时锚点按亲和性保留：left 留在左片，right 移到右片；
 * - applyEdits 的整批编辑全部基于原坐标（格式编辑不改变文本长度，
 *   内部按起点降序应用，保证原坐标始终有效）；
 * - 每个操作返回 undo，撤销恢复原文档对象，即恢复节点身份与锚点。
 */

import { normalize } from './normalize.js';
import { docLength, markKey, normalizeMarks } from './model.js';
import type { Doc, DocNode, Mark, MarkType } from './model.js';

export interface FormatEdit {
  kind: 'add' | 'remove';
  start: number;
  end: number;
  mark: Mark;
}

export type UndoFn = () => Doc;

export interface AppliedOp {
  doc: Doc;
  undo: UndoFn;
}

let idCounter = 0;

function nextId(): string {
  idCounter += 1;
  return 'n' + idCounter.toString(36);
}

function checkRange(doc: Doc, start: number, end: number): void {
  const length = docLength(doc);
  if (start < 0 || end < start || end > length) {
    throw new RangeError('invalid range [' + start + ', ' + end + ') for length ' + length);
  }
}

export function splitAt(doc: Doc, pos: number): Doc {
  const length = docLength(doc);
  if (pos <= 0 || pos >= length) return doc;
  const nodes: DocNode[] = [];
  let offset = 0;
  for (const node of doc.nodes) {
    const end = offset + node.text.length;
    if (offset < pos && pos < end) {
      const left: DocNode = { id: node.id, text: node.text.slice(0, pos - offset), marks: node.marks };
      const right: DocNode = { id: nextId(), text: node.text.slice(pos - offset), marks: node.marks };
      if (node.anchor) {
        if (node.anchor.affinity === 'left') left.anchor = node.anchor;
        else right.anchor = node.anchor;
      }
      nodes.push(left);
      nodes.push(right);
    } else {
      nodes.push(node);
    }
    offset = end;
  }
  return { nodes };
}

function withoutType(marks: Mark[], type: MarkType): Mark[] {
  const out: Mark[] = [];
  for (const mark of marks) {
    if (mark.type !== type) out.push(mark);
  }
  return out;
}

export function addMark(doc: Doc, start: number, end: number, mark: Mark): AppliedOp {
  checkRange(doc, start, end);
  if (start === end) return { doc, undo: () => doc };
  let d = splitAt(doc, start);
  d = splitAt(d, end);
  const nodes: DocNode[] = [];
  let offset = 0;
  for (const node of d.nodes) {
    const nodeEnd = offset + node.text.length;
    if (node.text.length > 0 && offset >= start && nodeEnd <= end) {
      let marks = node.marks;
      if (mark.type === 'link') marks = withoutType(marks, 'link');
      nodes.push({ id: node.id, text: node.text, marks: normalizeMarks(marks.concat([mark])), anchor: node.anchor });
    } else {
      nodes.push(node);
    }
    offset = nodeEnd;
  }
  const normalized = normalize({ nodes });
  return { doc: normalized.doc, undo: () => doc };
}

export function removeMark(doc: Doc, start: number, end: number, type: MarkType): AppliedOp {
  checkRange(doc, start, end);
  if (start === end) return { doc, undo: () => doc };
  let d = splitAt(doc, start);
  d = splitAt(d, end);
  const nodes: DocNode[] = [];
  let offset = 0;
  for (const node of d.nodes) {
    const nodeEnd = offset + node.text.length;
    if (node.text.length > 0 && offset >= start && nodeEnd <= end) {
      nodes.push({ id: node.id, text: node.text, marks: withoutType(node.marks, type), anchor: node.anchor });
    } else {
      nodes.push(node);
    }
    offset = nodeEnd;
  }
  const normalized = normalize({ nodes });
  return { doc: normalized.doc, undo: () => doc };
}

export function toggleMark(doc: Doc, start: number, end: number, mark: Mark): AppliedOp {
  checkRange(doc, start, end);
  if (start === end) return { doc, undo: () => doc };
  const key = markKey(mark);
  let offset = 0;
  let fullyApplied = true;
  for (const node of doc.nodes) {
    const nodeEnd = offset + node.text.length;
    if (node.text.length > 0 && nodeEnd > start && offset < end) {
      let has = false;
      for (const candidate of normalizeMarks(node.marks)) {
        if (markKey(candidate) === key) has = true;
      }
      if (!has) fullyApplied = false;
    }
    offset = nodeEnd;
  }
  if (fullyApplied) return removeMark(doc, start, end, mark.type);
  return addMark(doc, start, end, mark);
}

export function applyEdits(doc: Doc, edits: FormatEdit[]): AppliedOp {
  const order: number[] = [];
  for (let i = 0; i < edits.length; i++) order.push(i);
  order.sort((a, b) => edits[b].start - edits[a].start || edits[b].end - edits[a].end);
  let d = doc;
  for (const i of order) {
    const edit = edits[i];
    if (edit.kind === 'add') d = addMark(d, edit.start, edit.end, edit.mark).doc;
    else d = removeMark(d, edit.start, edit.end, edit.mark.type).doc;
  }
  return { doc: d, undo: () => doc };
}
