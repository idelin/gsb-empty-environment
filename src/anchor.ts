/**
 * 锚点迁移：把批注锚点随版本补丁迁移到新文档。
 *
 * 迁移规则（不猜选）：
 * - 补丁精确保留的位置 → 直接映射，输出唯一位置；
 * - 位置被删除/替换覆盖 → 用左右上下文在新文档中搜索：
 *     0 个匹配 → 失效，保留墓碑；
 *     1 个匹配 → 重定位到唯一位置；
 *     多个匹配 → 输出全部候选，绝不替用户挑选；
 * - 墓碑锚点每轮都用墓碑中保存的上下文快照重新搜索，
 *   唯一匹配时复活（撤销、文本搬移后归位都依赖这一点）。
 */

import { Patch, transformPosition } from './patch';

export type Bias = 'left' | 'right';

/** 锚点失去唯一位置时留下的墓碑，保存用于复活的上下文快照。 */
export interface Tombstone {
  /** 失去位置时所在的旧坐标（仅作记录，不随版本前进）。 */
  pos: number;
  left: string;
  right: string;
  cause: 'deleted' | 'ambiguous';
  /** cause 为 ambiguous 时，当时的全部候选位置。 */
  candidates: number[];
}

export interface Anchor {
  id: string;
  /** 当前文档版本中的 caret 位置；tombstone 不为 null 时此值已失效。 */
  pos: number;
  /** pos 恰好落在插入点时的偏向：'left' 不动，'right' 跟随插入文本之后。 */
  bias: Bias;
  contextLength: number;
  left: string;
  right: string;
  tombstone: Tombstone | null;
}

export type MigrationResult =
  | { kind: 'unique'; pos: number }
  | { kind: 'candidates'; positions: number[] }
  | { kind: 'invalid'; tombstone: Tombstone };

export interface Migration {
  anchor: Anchor;
  result: MigrationResult;
}

export interface AnchorOptions {
  id?: string;
  bias?: Bias;
  contextLength?: number;
}

export const DEFAULT_CONTEXT_LENGTH = 32;

let nextAnchorId = 1;

export function captureContext(
  doc: string,
  pos: number,
  contextLength: number
): { left: string; right: string } {
  return {
    left: doc.slice(Math.max(0, pos - contextLength), pos),
    right: doc.slice(pos, Math.min(doc.length, pos + contextLength)),
  };
}

export function createAnchor(doc: string, pos: number, options?: AnchorOptions): Anchor {
  if (!Number.isInteger(pos) || pos < 0 || pos > doc.length) {
    throw new RangeError('createAnchor: pos out of range');
  }
  const contextLength =
    options && options.contextLength !== undefined ? options.contextLength : DEFAULT_CONTEXT_LENGTH;
  const ctx = captureContext(doc, pos, contextLength);
  return {
    id: options && options.id ? options.id : 'anchor-' + nextAnchorId++,
    pos,
    bias: options && options.bias ? options.bias : 'left',
    contextLength,
    left: ctx.left,
    right: ctx.right,
    tombstone: null,
  };
}

export function contextMatchesAt(
  doc: string,
  pos: number,
  left: string,
  right: string
): boolean {
  return (
    doc.slice(pos - left.length, pos) === left &&
    doc.slice(pos, pos + right.length) === right
  );
}

/** 在新文档中查找所有满足左右上下文的位置（left+right 原本是连续子串）。 */
export function findContextPositions(doc: string, left: string, right: string): number[] {
  const needle = left + right;
  if (needle.length === 0) {
    const all: number[] = [];
    for (let p = 0; p <= doc.length; p++) all.push(p);
    return all;
  }
  const positions: number[] = [];
  let idx = doc.indexOf(needle);
  while (idx !== -1) {
    positions.push(idx + left.length);
    idx = doc.indexOf(needle, idx + 1);
  }
  return positions;
}

function liveAnchor(anchor: Anchor, doc: string, pos: number): Anchor {
  const ctx = captureContext(doc, pos, anchor.contextLength);
  return {
    id: anchor.id,
    pos,
    bias: anchor.bias,
    contextLength: anchor.contextLength,
    left: ctx.left,
    right: ctx.right,
    tombstone: null,
  };
}

/**
 * 把锚点随补丁迁移到新文档。newDoc 必须是 apply(oldDoc, patch) 的结果。
 * 返回新的锚点状态与迁移结果；输入锚点不被修改。
 */
export function migrateAnchor(anchor: Anchor, patch: Patch, newDoc: string): Migration {
  const tombstone = anchor.tombstone;
  if (tombstone) {
    // 墓碑锚点：用快照上下文在新版本中搜索，唯一匹配才复活。
    const matches = findContextPositions(newDoc, tombstone.left, tombstone.right);
    if (matches.length === 1) {
      const pos = matches[0];
      return { anchor: liveAnchor(anchor, newDoc, pos), result: { kind: 'unique', pos } };
    }
    if (matches.length > 1) {
      const next: Tombstone = {
        pos: tombstone.pos,
        left: tombstone.left,
        right: tombstone.right,
        cause: 'ambiguous',
        candidates: matches,
      };
      return {
        anchor: { ...anchor, tombstone: next },
        result: { kind: 'candidates', positions: matches },
      };
    }
    return { anchor, result: { kind: 'invalid', tombstone } };
  }

  // 活锚点：先走补丁的精确位置映射。
  const mapped = transformPosition(anchor.pos, patch, anchor.bias);
  if (mapped !== null) {
    return { anchor: liveAnchor(anchor, newDoc, mapped), result: { kind: 'unique', pos: mapped } };
  }

  // 位置被删除/替换覆盖：用上下文重定位，匹配数决定唯一、候选或失效。
  const matches = findContextPositions(newDoc, anchor.left, anchor.right);
  if (matches.length === 1) {
    const pos = matches[0];
    return { anchor: liveAnchor(anchor, newDoc, pos), result: { kind: 'unique', pos } };
  }
  const next: Tombstone = {
    pos: anchor.pos,
    left: anchor.left,
    right: anchor.right,
    cause: matches.length === 0 ? 'deleted' : 'ambiguous',
    candidates: matches,
  };
  const deadAnchor: Anchor = { ...anchor, tombstone: next };
  if (matches.length === 0) {
    return { anchor: deadAnchor, result: { kind: 'invalid', tombstone: next } };
  }
  return { anchor: deadAnchor, result: { kind: 'candidates', positions: matches } };
}

/** 批量迁移一组锚点；失效锚点以墓碑形式保留在返回数组中，不会被丢弃。 */
export function migrateAnchors(
  anchors: Anchor[],
  patch: Patch,
  newDoc: string
): { anchors: Anchor[]; results: MigrationResult[] } {
  const nextAnchors: Anchor[] = [];
  const results: MigrationResult[] = [];
  for (const anchor of anchors) {
    const migration = migrateAnchor(anchor, patch, newDoc);
    nextAnchors.push(migration.anchor);
    results.push(migration.result);
  }
  return { anchors: nextAnchors, results };
}
