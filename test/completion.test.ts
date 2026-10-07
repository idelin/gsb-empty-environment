import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  RangeAffinity,
  VersionedDocument,
  applyCompletion,
  range,
  resolveCompletionRanges,
  type CompletionItem,
} from '../src/index.ts';

/** 构造基于文档当前版本的补全项。 */
function completion(
  doc: VersionedDocument,
  replaceRange: { start: number; end: number },
  insertText: string,
  label = insertText,
): CompletionItem {
  return { label, insertText, replaceRange, version: doc.version };
}

describe('applyCompletion 基本流程', () => {
  it('当前版本的响应直接应用，且是一次可撤销事务', () => {
    const doc = new VersionedDocument('const he = 1;');
    const item = completion(doc, { start: 6, end: 8 }, 'hello');
    const result = applyCompletion(doc, item);
    assert.deepEqual(result, {
      ok: true,
      version: 1,
      appliedRanges: [range(6, 8)],
    });
    assert.equal(doc.text, 'const hello = 1;');
    doc.undo();
    assert.equal(doc.text, 'const he = 1;');
  });

  it('旧版本的响应沿编辑链映射后应用', () => {
    const doc = new VersionedDocument('he + other');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    // 响应返回后，用户在文档末尾继续输入（不影响替换范围）
    doc.insert(doc.length, '!');
    doc.insert(doc.length, '?');
    const result = applyCompletion(doc, item);
    assert.equal(result.ok, true);
    assert.equal(doc.text, 'hello + other!?');
  });

  it('用户在替换范围边界继续输入时，范围按 Expand 亲和性扩张', () => {
    const doc = new VersionedDocument('he');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    // 典型的“边输边补”：光标在词尾，用户又敲了一个 'l'
    doc.insert(2, 'l');
    const resolved = resolveCompletionRanges(doc, item);
    assert.deepEqual(resolved, { ok: true, ranges: [range(0, 3)] });
    const result = applyCompletion(doc, item);
    assert.equal(result.ok, true);
    assert.equal(doc.text, 'hello');
  });

  it('后续编辑触及替换范围内部则响应失效', () => {
    const doc = new VersionedDocument('he + 1');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    doc.replace(1, 2, 'a'); // 把 "he" 改成 "ha"，触及替换范围内部
    const result = applyCompletion(doc, item);
    assert.deepEqual(result, { ok: false, reason: 'invalidated' });
    assert.equal(doc.text, 'ha + 1'); // 文档未被改动
  });

  it('替换范围整体被删除则响应失效', () => {
    const doc = new VersionedDocument('he + 1');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    doc.delete(0, 2);
    assert.deepEqual(applyCompletion(doc, item), { ok: false, reason: 'invalidated' });
  });

  it('来自未来版本的响应被拒绝', () => {
    const doc = new VersionedDocument('he');
    const item: CompletionItem = {
      label: 'hello',
      insertText: 'hello',
      replaceRange: range(0, 2),
      version: doc.version + 1,
    };
    assert.deepEqual(applyCompletion(doc, item), { ok: false, reason: 'future-version' });
    assert.equal(doc.text, 'he');
  });

  it('Collapse 亲和性下边界输入不被吸收', () => {
    const doc = new VersionedDocument('he');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    doc.insert(2, 'l');
    const resolved = resolveCompletionRanges(doc, item, [], RangeAffinity.Collapse);
    assert.deepEqual(resolved, { ok: true, ranges: [range(0, 2)] });
  });
});

describe('多光标补全', () => {
  it('主从范围一起映射并作为一次事务应用', () => {
    const doc = new VersionedDocument('he + he');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    const result = applyCompletion(doc, item, [range(5, 7)]);
    assert.equal(result.ok, true);
    assert.equal(doc.text, 'hello + hello');
    // 单次撤销即回退全部光标的替换
    doc.undo();
    assert.equal(doc.text, 'he + he');
  });

  it('次要范围同样基于响应版本，随后续编辑一起映射', () => {
    const doc = new VersionedDocument('he + he.');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    doc.insert(doc.length, ' tail'); // 响应返回后在末尾（范围之外）追加文本
    const result = applyCompletion(doc, item, [range(5, 7)]);
    assert.equal(result.ok, true);
    assert.equal(doc.text, 'hello + hello. tail');
  });

  it('任一次要范围被后续编辑触及则整体失效', () => {
    const doc = new VersionedDocument('he + he');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    doc.replace(6, 7, 'a'); // 触及次要范围 [5,7) 内部
    const result = applyCompletion(doc, item, [range(5, 7)]);
    assert.deepEqual(result, { ok: false, reason: 'invalidated' });
    assert.equal(doc.text, 'he + ha');
  });

  it('映射后范围重叠时拒绝应用', () => {
    const doc = new VersionedDocument('he');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    // 次要范围与主范围重叠
    assert.deepEqual(applyCompletion(doc, item, [range(1, 2)]), {
      ok: false,
      reason: 'overlapping-ranges',
    });
    // 两个光标映射到同一位置也算冲突
    assert.deepEqual(applyCompletion(doc, item, [range(0, 2)]), {
      ok: false,
      reason: 'overlapping-ranges',
    });
    assert.equal(doc.text, 'he');
  });

  it('Expand 下边界输入被撤销时，因删除触及已扩张范围而失效', () => {
    const doc = new VersionedDocument('he');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    doc.insert(2, 'y'); // 边界输入被 Expand 吸收：范围扩张为 [0,3)
    doc.undo(); // 撤销该输入会删除扩张后范围的内部
    assert.deepEqual(applyCompletion(doc, item), { ok: false, reason: 'invalidated' });
  });

  it('Collapse 下撤销边界输入后，旧响应仍可按编辑链核对并应用', () => {
    const doc = new VersionedDocument('he');
    const item = completion(doc, { start: 0, end: 2 }, 'hello');
    doc.insert(2, 'y'); // Collapse 不吸收边界输入，范围保持 [0,2)
    doc.undo();
    const result = applyCompletion(doc, item, [], RangeAffinity.Collapse);
    assert.equal(result.ok, true);
    assert.equal(doc.text, 'hello');
  });
});
