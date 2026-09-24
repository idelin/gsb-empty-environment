/**
 * reversible-patch — 带上下文校验的可逆文本补丁
 *
 * 坐标约定：所有偏移/长度均为 UTF-16 码元（即 JS 字符串下标）。
 * 任何补丁边界都不允许切开代理对（surrogate pair）。
 *
 * 操作模型（针对基线文本顺序消费的 op 流）：
 *   retain(n)            — 原样保留 n 个码元
 *   delete(n, removed, removedId?) — 删除 n 个码元，removed 为被删原文（上下文校验用），
 *                                    removedId 为被删文本来源 insert 操作的身份（可逆组合用）
 *   insert(text, id) — 插入文本，id 为该编辑操作的唯一身份；
 *                      相同文本由不同操作插入时不会被视为同一次编辑
 */
import { createHash, randomUUID } from 'node:crypto';

export type Op =
  | { kind: 'retain'; count: number }
  | { kind: 'delete'; count: number; removed: string; removedId?: string }
  | { kind: 'insert'; text: string; id: string };

export interface Patch {
  /** 基线文本摘要（SHA-256，UTF-8 字节） */
  baseDigest: string;
  /** 基线文本长度（UTF-16 码元数） */
  baseLength: number;
  /** 结果文本摘要；未知时缺省（apply 仅在存在时校验） */
  resultDigest?: string;
  resultLength?: number;
  ops: Op[];
}

export class PatchError extends Error {}
export class DigestMismatchError extends PatchError {}
export class ContextMismatchError extends PatchError {}
export class SurrogateBoundaryError extends PatchError {}
export class ComposeError extends PatchError {}
export class ConflictError extends PatchError {}
export class TransformError extends PatchError {}
export class CommutationError extends PatchError {}

export type IdGen = () => string;
export const defaultIdGen: IdGen = () => randomUUID();

/** 文本摘要：SHA-256(UTF-8 字节)。长度相同但内容不同的文本摘要必然不同。 */
export function digest(text: string): string {
  return createHash('sha256').update(Buffer.from(text, 'utf8')).digest('hex');
}

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}
function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}
/** 在 pos 处切分是否会切开 text 中的代理对 */
export function splitsSurrogatePair(text: string, pos: number): boolean {
  return (
    pos > 0 &&
    pos < text.length &&
    isHighSurrogate(text.charCodeAt(pos - 1)) &&
    isLowSurrogate(text.charCodeAt(pos))
  );
}
function assertBoundary(text: string, pos: number): void {
  if (splitsSurrogatePair(text, pos)) {
    throw new SurrogateBoundaryError(
      `offset ${pos} splits a surrogate pair; offsets must be UTF-16 code-unit boundaries outside pairs`,
    );
  }
}

function remaining(op: Op): number {
  return op.kind === 'insert' ? op.text.length : op.count;
}

/** 规范化：去掉零长 retain，合并相邻 retain。 */
export function normalize(ops: Op[]): Op[] {
  const out: Op[] = [];
  for (const op of ops) {
    if (op.kind === 'retain') {
      if (op.count === 0) continue;
      const last = out[out.length - 1];
      if (last && last.kind === 'retain') {
        last.count += op.count;
        continue;
      }
    }
    out.push(op.kind === 'retain' ? { ...op } : op);
  }
  return out;
}

/** 由基线与编辑后文本构造补丁（公共前后缀 diff，代理对安全）。 */
export function makePatch(base: string, edited: string, idGen: IdGen = defaultIdGen): Patch {
  let start = 0;
  const minLen = Math.min(base.length, edited.length);
  while (start < minLen && base[start] === edited[start]) start++;
  while (splitsSurrogatePair(base, start) || splitsSurrogatePair(edited, start)) start--;

  let suffix = 0;
  while (
    suffix < base.length - start &&
    suffix < edited.length - start &&
    base[base.length - 1 - suffix] === edited[edited.length - 1 - suffix]
  ) {
    suffix++;
  }
  // 代理对安全：两侧同步收缩公共后缀，保持两侧后缀始终相等
  while (
    suffix > 0 &&
    (splitsSurrogatePair(base, base.length - suffix) ||
      splitsSurrogatePair(edited, edited.length - suffix))
  ) {
    suffix--;
  }
  const endBase = base.length - suffix;
  const endEdited = edited.length - suffix;

  const ops: Op[] = [];
  if (start > 0) ops.push({ kind: 'retain', count: start });
  if (endBase > start) {
    ops.push({ kind: 'delete', count: endBase - start, removed: base.slice(start, endBase) });
  }
  if (endEdited > start) {
    ops.push({ kind: 'insert', text: edited.slice(start, endEdited), id: idGen() });
  }
  if (base.length - endBase > 0) ops.push({ kind: 'retain', count: base.length - endBase });

  return {
    baseDigest: digest(base),
    baseLength: base.length,
    resultDigest: digest(edited),
    resultLength: edited.length,
    ops: normalize(ops),
  };
}

/**
 * 应用补丁。先验证基线摘要与长度，再逐个校验 delete 的上下文原文；
 * 任何一步失败都抛错，不返回部分结果（字符串不可变，调用方持有的原文不受影响）。
 */
export function apply(base: string, patch: Patch): string {
  if (base.length !== patch.baseLength || digest(base) !== patch.baseDigest) {
    throw new DigestMismatchError(
      'base text digest/length mismatch: patch was created against a different document',
    );
  }
  let pos = 0;
  let out = '';
  for (const op of patch.ops) {
    assertBoundary(base, pos);
    if (op.kind === 'retain') {
      if (op.count < 0 || pos + op.count > base.length) {
        throw new ContextMismatchError(`retain(${op.count}) out of bounds at ${pos}`);
      }
      out += base.slice(pos, pos + op.count);
      pos += op.count;
    } else if (op.kind === 'delete') {
      if (op.count < 0 || pos + op.count > base.length) {
        throw new ContextMismatchError(`delete(${op.count}) out of bounds at ${pos}`);
      }
      if (op.removed.length !== op.count || base.slice(pos, pos + op.count) !== op.removed) {
        throw new ContextMismatchError(
          `delete context mismatch at ${pos}: expected ${JSON.stringify(op.removed)}`,
        );
      }
      pos += op.count;
    } else {
      out += op.text;
    }
  }
  assertBoundary(base, pos);
  if (pos !== base.length) {
    throw new ContextMismatchError(`ops consume ${pos} of ${base.length} code units`);
  }
  if (patch.resultLength !== undefined && out.length !== patch.resultLength) {
    throw new ContextMismatchError('result length mismatch');
  }
  if (patch.resultDigest !== undefined && digest(out) !== patch.resultDigest) {
    throw new DigestMismatchError('result digest mismatch');
  }
  return out;
}

/** 求逆：insert↔delete 互换（delete 恢复为 insert，insert 变为带 removedId 的 delete）。 */
export function invert(patch: Patch, idGen: IdGen = defaultIdGen): Patch {
  if (patch.resultDigest === undefined || patch.resultLength === undefined) {
    throw new PatchError('cannot invert: patch lacks result digest');
  }
  const ops: Op[] = patch.ops.map((op) => {
    if (op.kind === 'retain') return { ...op };
    if (op.kind === 'delete') return { kind: 'insert', text: op.removed, id: idGen() };
    return { kind: 'delete', count: op.text.length, removed: op.text, removedId: op.id };
  });
  return {
    baseDigest: patch.resultDigest,
    baseLength: patch.resultLength,
    resultDigest: patch.baseDigest,
    resultLength: patch.baseLength,
    ops,
  };
}

/**
 * 顺序组合：compose(P, Q) 满足 apply(b, compose(P,Q)) === apply(apply(b,P),Q)。
 * 组合前校验 P 的结果摘要等于 Q 的基线摘要（上下文校验）。
 * 相邻互逆操作（insert 后紧跟删除同一插入文本的 delete，身份一致）相互消除；
 * 未被消除的 delete 保留其 removedId（后续操作引用的坐标来源）。
 */
export function compose(P: Patch, Q: Patch): Patch {
  if (P.resultDigest === undefined || P.resultDigest !== Q.baseDigest) {
    throw new ComposeError('P.resultDigest !== Q.baseDigest: patches are not composable');
  }
  const ops: Op[] = [];
  let pi = 0;
  let qi = 0;
  let pOff = 0;
  let qOff = 0;

  const advanceP = (n: number): void => {
    pOff += n;
    if (pi < P.ops.length && pOff >= remaining(P.ops[pi])) {
      pi++;
      pOff = 0;
    }
  };
  const advanceQ = (n: number): void => {
    qOff += n;
    if (qi < Q.ops.length && qOff >= remaining(Q.ops[qi])) {
      qi++;
      qOff = 0;
    }
  };

  while (pi < P.ops.length || qi < Q.ops.length) {
    const p = P.ops[pi] as Op | undefined;
    const q = Q.ops[qi] as Op | undefined;
    if (p && q && p.kind === 'delete' && q.kind === 'insert') {
      // 互逆消除（delete 后紧跟同文 insert）：净效果为 retain，坐标来源回到基线
      const take = Math.min(p.count - pOff, q.text.length - qOff);
      if (take > 0 && p.removed.slice(pOff, pOff + take) === q.text.slice(qOff, qOff + take)) {
        ops.push({ kind: 'retain', count: take });
        advanceP(take);
        advanceQ(take);
        continue;
      }
    }
    if (p && p.kind === 'delete') {
      ops.push(p);
      pi++;
      pOff = 0;
      continue;
    }
    if (q && q.kind === 'insert') {
      ops.push(q);
      qi++;
      qOff = 0;
      continue;
    }
    if (!p || !q) {
      throw new ComposeError('length mismatch: patches do not line up');
    }
    const take = Math.min(remaining(p) - pOff, remaining(q) - qOff);
    if (p.kind === 'insert' && q.kind === 'retain') {
      ops.push({ kind: 'insert', text: p.text.slice(pOff, pOff + take), id: p.id });
    } else if (p.kind === 'insert' && q.kind === 'delete') {
      const ins = p.text.slice(pOff, pOff + take);
      const del = q.removed.slice(qOff, qOff + take);
      if (q.removedId !== undefined && q.removedId !== p.id) {
        throw new ComposeError(
          `identity mismatch: delete targets op ${q.removedId} but found insert ${p.id}`,
        );
      }
      if (ins !== del) {
        throw new ComposeError('cancel mismatch: deleted text does not match inserted text');
      }
      // 互逆消除：双方各消费 take，不产生任何输出
    } else if (p.kind === 'retain' && q.kind === 'retain') {
      ops.push({ kind: 'retain', count: take });
    } else if (p.kind === 'retain' && q.kind === 'delete') {
      const del: Op = {
        kind: 'delete',
        count: take,
        removed: q.removed.slice(qOff, qOff + take),
      };
      if (q.removedId !== undefined) del.removedId = q.removedId;
      ops.push(del);
    } else {
      throw new ComposeError('unreachable op pairing');
    }
    advanceP(take);
    advanceQ(take);
  }

  return {
    baseDigest: P.baseDigest,
    baseLength: P.baseLength,
    resultDigest: Q.resultDigest,
    resultLength: Q.resultLength,
    ops: normalize(ops),
  };
}

/**
 * 坐标变换 / 三方重定位：P、Q 基于同一 base 的并发编辑，
 * 返回 P' 使得 P' 可应用于 apply(base, Q) 之后。
 * insert-insert 同位置按操作身份 id 字典序定序（相同文本不同身份互不混淆）；
 * delete-delete 区间相交抛 ConflictError。
 */
export function transform(P: Patch, Q: Patch, base: string): Patch {
  if (P.baseDigest !== Q.baseDigest) {
    throw new TransformError('patches are based on different documents');
  }
  if (base.length !== P.baseLength || digest(base) !== P.baseDigest) {
    throw new DigestMismatchError('base does not match patch base digest');
  }
  const ops: Op[] = [];
  let pi = 0;
  let qi = 0;
  let pOff = 0;
  let qOff = 0;

  const advanceP = (n: number): void => {
    pOff += n;
    if (pi < P.ops.length && pOff >= remaining(P.ops[pi])) {
      pi++;
      pOff = 0;
    }
  };
  const advanceQ = (n: number): void => {
    qOff += n;
    if (qi < Q.ops.length && qOff >= remaining(Q.ops[qi])) {
      qi++;
      qOff = 0;
    }
  };

  while (pi < P.ops.length || qi < Q.ops.length) {
    const p = P.ops[pi] as Op | undefined;
    const q = Q.ops[qi] as Op | undefined;
    if (p && q && p.kind === 'insert' && q.kind === 'insert') {
      if (p.id <= q.id) {
        ops.push(p);
        pi++;
        pOff = 0;
      } else {
        ops.push({ kind: 'retain', count: q.text.length });
        qi++;
        qOff = 0;
      }
      continue;
    }
    if (p && p.kind === 'insert') {
      ops.push(p);
      pi++;
      pOff = 0;
      continue;
    }
    if (q && q.kind === 'insert') {
      ops.push({ kind: 'retain', count: q.text.length });
      qi++;
      qOff = 0;
      continue;
    }
    if (!p) {
      // P 已耗尽，视为无限 retain：Q 的 delete 直接丢弃，retain 透传
      const qq = q as Op;
      if (qq.kind === 'retain') ops.push({ kind: 'retain', count: qq.count - qOff });
      advanceQ(remaining(qq) - qOff);
      continue;
    }
    if (!q) {
      const pp = p as Op;
      if (pp.kind === 'retain') {
        ops.push({ kind: 'retain', count: pp.count - pOff });
      } else if (pp.kind === 'delete') {
        const del: Op = {
          kind: 'delete',
          count: pp.count - pOff,
          removed: pp.removed.slice(pOff),
        };
        if (pp.removedId !== undefined) del.removedId = pp.removedId;
        ops.push(del);
      }
      advanceP(remaining(pp) - pOff);
      continue;
    }
    if (p.kind === 'delete' && q.kind === 'delete') {
      throw new ConflictError('overlapping deletes conflict');
    }
    const take = Math.min(remaining(p) - pOff, remaining(q) - qOff);
    if (p.kind === 'delete') {
      const del: Op = { kind: 'delete', count: take, removed: p.removed.slice(pOff, pOff + take) };
      if (p.removedId !== undefined) del.removedId = p.removedId;
      ops.push(del);
    } else if (q.kind === 'delete') {
      // P 的 retain 落在 Q 删除的区间内：丢弃
    } else {
      ops.push({ kind: 'retain', count: take });
    }
    advanceP(take);
    advanceQ(take);
  }

  const partial: Patch = {
    baseDigest: Q.resultDigest as string,
    baseLength: Q.resultLength as number,
    ops: normalize(ops),
  };
  // 借助 base 计算变换后补丁的结果摘要（同时自检可应用性）
  const afterQ = apply(base, Q);
  const merged = apply(afterQ, partial);
  partial.resultDigest = digest(merged);
  partial.resultLength = merged.length;
  return partial;
}

/** 三方重定位：把基于 base 的补丁 mine 重定位到并发编辑 theirs 的结果之上。 */
export function relocate(mine: Patch, theirs: Patch, base: string): Patch {
  return transform(mine, theirs, base);
}

/**
 * 交换图验证：对不相交编辑，apply(apply(b,P),transform(Q,P,b))
 * 必须等于 apply(apply(b,Q),transform(P,Q,b))；交叉删除抛 ConflictError。
 */
export function validateCommutation(P: Patch, Q: Patch, base: string): void {
  const pOverQ = transform(P, Q, base);
  const qOverP = transform(Q, P, base);
  const left = apply(apply(base, Q), pOverQ);
  const right = apply(apply(base, P), qOverP);
  if (left !== right) {
    throw new CommutationError('commutative diagram does not hold');
  }
}
