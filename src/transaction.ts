import type { AppliedDocEdit, DocSnapshot, ReplaceOp, TxnResult, VersionConflict } from './types.js';
import { replaceAll } from './replace.js';

interface DocState {
  text: string;
  version: number;
}

// 简单的文档存储：每次写入递增版本号，版本即冻结点。
export class DocumentStore {
  _docs: Map<string, DocState>;

  constructor() {
    this._docs = new Map();
  }

  register(id: string, text: string): DocSnapshot {
    const state: DocState = { text, version: 0 };
    this._docs.set(id, state);
    return { text, version: 0 };
  }

  has(id: string): boolean {
    return this._docs.has(id);
  }

  get(id: string): DocSnapshot | undefined {
    const s = this._docs.get(id);
    if (s === undefined) {
      return undefined;
    }
    return { text: s.text, version: s.version };
  }

  // 事务外的写入（模拟并发修改），递增版本。
  write(id: string, text: string): number {
    const s = this._docs.get(id);
    if (s === undefined) {
      throw new Error('unknown document: ' + id);
    }
    s.text = text;
    s.version += 1;
    return s.version;
  }

  // 恢复到指定文本与版本（仅供 undo 使用）。
  restore(id: string, text: string, version: number): void {
    const s = this._docs.get(id);
    if (s === undefined) {
      throw new Error('unknown document: ' + id);
    }
    s.text = text;
    s.version = version;
  }
}

// 多文档事务：所有操作针对同一冻结版本校验，任一文档版本漂移则整批拒绝。
// 成功时一次性应用全部文档，并返回可逆编辑、范围映射与 undo。
export function commitTransaction(store: DocumentStore, ops: ReplaceOp[]): TxnResult {
  const conflicts: VersionConflict[] = [];
  const problems: string[] = [];
  const seen: Set<string> = new Set();

  for (const op of ops) {
    if (seen.has(op.docId)) {
      problems.push('duplicate op for document: ' + op.docId);
      continue;
    }
    seen.add(op.docId);
    const snap = store.get(op.docId);
    if (snap === undefined) {
      problems.push('unknown document: ' + op.docId);
      continue;
    }
    if (snap.version !== op.expectedVersion) {
      const conflict: VersionConflict = {
        docId: op.docId,
        expectedVersion: op.expectedVersion,
        actualVersion: snap.version,
      };
      conflicts.push(conflict);
    }
  }

  if (problems.length > 0 || conflicts.length > 0) {
    let reason = problems.join('; ');
    if (reason === '') {
      reason = 'version conflict';
    }
    return { ok: false, reason, conflicts };
  }

  // 在冻结文本上纯计算替换结果，不写库。
  const plans: AppliedDocEdit[] = [];
  for (const op of ops) {
    const snap = store.get(op.docId);
    if (snap === undefined) {
      return { ok: false, reason: 'unknown document: ' + op.docId, conflicts: [] };
    }
    const result = replaceAll(snap.text, op.pattern, op.replacement);
    const plan: AppliedDocEdit = {
      docId: op.docId,
      oldVersion: snap.version,
      newVersion: snap.version + 1,
      oldText: snap.text,
      newText: result.newText,
      edits: result.edits,
      result,
    };
    plans.push(plan);
  }

  // 应用前二次校验：计算期间版本仍须冻结。
  for (const p of plans) {
    const cur = store.get(p.docId);
    if (cur === undefined || cur.version !== p.oldVersion) {
      const conflict: VersionConflict = {
        docId: p.docId,
        expectedVersion: p.oldVersion,
        actualVersion: cur === undefined ? -1 : cur.version,
      };
      return { ok: false, reason: 'version changed during commit', conflicts: [conflict] };
    }
  }

  for (const p of plans) {
    store.write(p.docId, p.newText);
  }

  let undone = false;
  const undo = (): void => {
    if (undone) {
      throw new Error('transaction already undone');
    }
    for (const p of plans) {
      const cur = store.get(p.docId);
      if (cur === undefined || cur.version !== p.newVersion) {
        throw new Error('cannot undo: document modified after commit: ' + p.docId);
      }
    }
    for (const p of plans) {
      store.restore(p.docId, p.oldText, p.oldVersion);
    }
    undone = true;
  };

  return { ok: true, applied: plans, undo };
}
