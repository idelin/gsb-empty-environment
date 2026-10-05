/**
 * 测试：npm test（先 tsc 编译，再用 node 运行本文件）。
 */
import { strict as assert } from 'assert';
import { combineFixes } from '../src/combine';
import { textContains } from '../src/preconditions';
import { FixSuggestion, Rejection } from '../src/types';

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed += 1;
    console.log(`  ok - ${name}`);
  } catch (err) {
    failed += 1;
    console.error(`  FAIL - ${name}`);
    console.error(err);
  }
}

function rejectionOf(result: ReturnType<typeof combineFixes>, id: string): Rejection {
  const r = result.rejected.find((x) => x.id === id);
  assert.ok(r, `应存在 ${id} 的未选中记录`);
  return r as Rejection;
}

const edit = (start: number, end: number, replacement: string, file = 'f.ts') => ({
  file, start, end, replacement,
});

// ---------------------------------------------------------------------------
console.log('冲突检测与选择');

test('文本区间重叠的两条建议不能同时选中，高收益者入选且另一条有冲突证据', () => {
  const suggestions: FixSuggestion[] = [
    { id: 'A', description: 'a', benefit: 10, edits: [edit(0, 10, 'x'.repeat(10))] },
    { id: 'B', description: 'b', benefit: 5, edits: [edit(5, 15, 'y'.repeat(10))] },
  ];
  const result = combineFixes({ 'f.ts': 'z'.repeat(30) }, suggestions);
  assert.deepStrictEqual(result.selected.map((s) => s.id), ['A']);
  assert.strictEqual(result.totalBenefit, 10);
  const r = rejectionOf(result, 'B');
  const conflict = r.reasons.find((x) => x.kind === 'conflict');
  assert.ok(conflict && conflict.kind === 'conflict');
  assert.strictEqual(conflict.conflicts[0].kind, 'text-overlap');
  assert.ok(conflict.conflicts[0].detail.includes('[5, 15)'));
});

test('文本范围不重叠但改写同一符号，仍判定不兼容', () => {
  const suggestions: FixSuggestion[] = [
    { id: 'A', description: 'a', benefit: 10, edits: [edit(0, 5, 'aaaaa')], writesSymbols: ['foo'] },
    { id: 'B', description: 'b', benefit: 6, edits: [edit(20, 25, 'bbbbb')], writesSymbols: ['foo'] },
  ];
  const result = combineFixes({ 'f.ts': 'z'.repeat(30) }, suggestions);
  assert.deepStrictEqual(result.selected.map((s) => s.id), ['A']);
  const r = rejectionOf(result, 'B');
  const conflict = r.reasons.find((x) => x.kind === 'conflict');
  assert.ok(conflict && conflict.kind === 'conflict');
  assert.strictEqual(conflict.conflicts[0].kind, 'symbol-write-write');
});

test('写-读冲突在无前置排序时不兼容，有 requires 排序时可共存', () => {
  const writer: FixSuggestion = {
    id: 'W', description: 'w', benefit: 10, edits: [edit(0, 5, 'aaaaa')], writesSymbols: ['foo'],
  };
  const reader: FixSuggestion = {
    id: 'R', description: 'r', benefit: 6, edits: [edit(20, 25, 'bbbbb')], readsSymbols: ['foo'],
  };
  const unordered = combineFixes({ 'f.ts': 'z'.repeat(30) }, [writer, reader]);
  assert.deepStrictEqual(unordered.selected.map((s) => s.id), ['W']);

  const ordered = combineFixes({ 'f.ts': 'z'.repeat(30) }, [writer, { ...reader, requires: ['W'] }]);
  assert.deepStrictEqual(
    ordered.selected.map((s) => s.id).sort(),
    ['R', 'W'],
  );
  assert.strictEqual(ordered.totalBenefit, 16);
});

test('组合优化：单条高收益不如两条可共存的低收益时选后者', () => {
  const suggestions: FixSuggestion[] = [
    { id: 'A', description: 'a', benefit: 5, edits: [edit(0, 5, 'aaaaa')] },
    { id: 'B', description: 'b', benefit: 4, edits: [edit(0, 5, 'bbbbb')] },
    { id: 'C', description: 'c', benefit: 4, edits: [edit(20, 25, 'ccccc')] },
  ];
  // A 与 B 冲突；A 与 C 也冲突（同一区间平移到这里）；B 与 C 不冲突。
  suggestions[2].edits = [edit(20, 25, 'ccccc')];
  suggestions[0].edits = [edit(0, 5, 'aaaaa'), edit(20, 25, 'aaaaa')];
  const result = combineFixes({ 'f.ts': 'z'.repeat(30) }, suggestions);
  assert.deepStrictEqual(result.selected.map((s) => s.id).sort(), ['B', 'C']);
  assert.strictEqual(result.totalBenefit, 8);
});

// ---------------------------------------------------------------------------
console.log('前置关系');

test('选中依赖者会自动带上其前置（前置闭包闭合）', () => {
  const suggestions: FixSuggestion[] = [
    { id: 'BASE', description: 'base', benefit: 1, edits: [edit(0, 5, 'aaaaa')] },
    { id: 'DEP', description: 'dep', benefit: 20, requires: ['BASE'], edits: [edit(10, 15, 'bbbbb')] },
  ];
  const result = combineFixes({ 'f.ts': 'z'.repeat(30) }, suggestions);
  assert.deepStrictEqual(result.selected.map((s) => s.id).sort(), ['BASE', 'DEP']);
  // 前置必须先应用
  assert.ok(
    (result.selected.find((s) => s.id === 'BASE')?.order ?? 1) <
      (result.selected.find((s) => s.id === 'DEP')?.order ?? 0),
  );
});

test('前置本身不可行时依赖者也不可行，并给出证据', () => {
  const suggestions: FixSuggestion[] = [
    { id: 'BASE', description: 'base', benefit: 1, edits: [edit(0, 5, 'aaaaa')], requires: ['GHOST'] },
    { id: 'DEP', description: 'dep', benefit: 20, requires: ['BASE'], edits: [edit(10, 15, 'bbbbb')] },
  ];
  const result = combineFixes({ 'f.ts': 'z'.repeat(30) }, suggestions);
  assert.strictEqual(result.selected.length, 0);
  assert.ok(rejectionOf(result, 'BASE').reasons.some((r) => r.kind === 'unknown-prerequisite'));
  assert.ok(rejectionOf(result, 'DEP').reasons.some((r) => r.kind === 'missing-prerequisite'));
});

test('前置环被拒绝并给出环证据', () => {
  const suggestions: FixSuggestion[] = [
    { id: 'X', description: 'x', benefit: 1, requires: ['Y'], edits: [edit(0, 5, 'aaaaa')] },
    { id: 'Y', description: 'y', benefit: 1, requires: ['X'], edits: [edit(10, 15, 'bbbbb')] },
  ];
  const result = combineFixes({ 'f.ts': 'z'.repeat(30) }, suggestions);
  assert.strictEqual(result.selected.length, 0);
  const r = rejectionOf(result, 'X');
  const cycle = r.reasons.find((x) => x.kind === 'prerequisite-cycle');
  assert.ok(cycle && cycle.kind === 'prerequisite-cycle');
  assert.deepStrictEqual([...new Set(cycle.cycle)].sort(), ['X', 'Y']);
});

// ---------------------------------------------------------------------------
console.log('数量上限');

test('最多 25 项：30 条无冲突建议只选收益最高的 25 条，其余有收益证据', () => {
  const suggestions: FixSuggestion[] = [];
  for (let i = 0; i < 30; i++) {
    suggestions.push({
      id: `S${i}`,
      description: `s${i}`,
      benefit: i + 1, // S29 收益最高
      edits: [edit(i * 10, i * 10 + 5, 'aaaaa')],
    });
  }
  const result = combineFixes({ 'f.ts': 'z'.repeat(400) }, suggestions);
  assert.strictEqual(result.selected.length, 25);
  assert.strictEqual(result.totalBenefit, (6 + 30) * 25 / 2);
  assert.strictEqual(result.rejected.length, 5);
  for (const r of result.rejected) {
    assert.ok(r.reasons.some((x) => x.kind === 'benefit-too-low'), `${r.id} 应有 benefit-too-low 证据`);
  }
});

// ---------------------------------------------------------------------------
console.log('应用与重检');

test('应用后重检：前置条件被先应用的修复破坏时被丢弃，依赖者连带丢弃', () => {
  const content = 'keep-this-marker and other text here';
  const suggestions: FixSuggestion[] = [
    {
      id: 'REMOVE', description: '移除标记', benefit: 100,
      edits: [edit(0, 16, 'gone')], // 删掉 keep-this-marker
    },
    {
      id: 'NEED', description: '依赖标记', benefit: 50,
      edits: [edit(28, 32, 'TEXT')],
      preconditions: [textContains('f.ts', 'keep-this-marker', '标记 keep-this-marker 仍存在')],
    },
    {
      id: 'NEED2', description: '依赖 NEED', benefit: 40,
      requires: ['NEED'],
      edits: [edit(34, 36, 'XY')],
    },
  ];
  const result = combineFixes({ 'f.ts': content }, suggestions);
  assert.deepStrictEqual(result.selected.map((s) => s.id), ['REMOVE']);
  const need = rejectionOf(result, 'NEED');
  const failed = need.reasons.find((r) => r.kind === 'post-apply-precondition-failed');
  assert.ok(failed && failed.kind === 'post-apply-precondition-failed');
  assert.deepStrictEqual(failed.invalidatedBy, ['REMOVE']);
  assert.ok(rejectionOf(result, 'NEED2').reasons.some((r) => r.kind === 'dependent-dropped'));
});

test('多条非重叠编辑应用到同一文件时偏移平移正确', () => {
  const content = '0123456789ABCDEFGHIJ';
  const suggestions: FixSuggestion[] = [
    { id: 'E1', description: 'e1', benefit: 5, edits: [edit(0, 2, '*****')] },   // 变长 +3
    { id: 'E2', description: 'e2', benefit: 4, edits: [edit(10, 12, '##')] },     // 位于 E1 之后
    { id: 'E3', description: 'e3', benefit: 3, edits: [edit(18, 20, '@')] },      // 缩短 -1
  ];
  const result = combineFixes({ 'f.ts': content }, suggestions);
  assert.strictEqual(result.selected.length, 3);
  // 期望：'01'->'*****'，'AB'->'##'，'IJ'->'@'
  assert.strictEqual(result.finalFiles['f.ts'], '*****23456789##CDEFGH@');
});

test('初始前置条件不满足直接拒绝并给出证据', () => {
  const suggestions: FixSuggestion[] = [
    {
      id: 'P', description: 'p', benefit: 5,
      edits: [edit(0, 1, 'a')],
      preconditions: [textContains('f.ts', 'nonexistent-marker')],
    },
  ];
  const result = combineFixes({ 'f.ts': 'hello' }, suggestions);
  assert.strictEqual(result.selected.length, 0);
  assert.ok(rejectionOf(result, 'P').reasons.some((r) => r.kind === 'initial-precondition-failed'));
});

// ---------------------------------------------------------------------------
console.log(`\n${passed} 通过, ${failed} 失败`);
if (failed > 0) process.exit(1);
