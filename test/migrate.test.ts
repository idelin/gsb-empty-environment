import { test } from 'node:test';
import assert from 'node:assert/strict';
import { migrateCoverage } from '../src/migrate.ts';
import { fingerprintOf } from '../src/fingerprint.ts';
import type { NewStatement, OldStatement, SourceRange } from '../src/types.ts';

function range(file: string, line: number): SourceRange {
  return { file, start: { line, column: 0 }, end: { line, column: 10 } };
}

function oldStmt(id: string, source: string, hits: number, line = 1): OldStatement {
  return { id, range: range('a.ts', line), hits, fingerprint: fingerprintOf(source) };
}

function newStmt(id: string, source: string, line = 1): NewStatement {
  return { id, range: range('a.ts', line), fingerprint: fingerprintOf(source) };
}

test('纯移动：内容指纹一致即可继承，与行号无关', () => {
  const report = migrateCoverage(
    [oldStmt('s1', 'foo();', 7, 10)],
    [newStmt('s1', 'foo();', 42)],
  );
  assert.equal(report.inherited.length, 1);
  assert.equal(report.inherited[0].via, 'auto-identical');
  assert.equal(report.inherited[0].hits, 7);
  assert.deepEqual(report.inherited[0].sources, [{ oldId: 's1', hits: 7 }]);
  assert.equal(report.added.length, 0);
  assert.equal(report.uncertain.length, 0);
});

test('禁止行号平移：同一位置但内容不同的语句不得继承', () => {
  const report = migrateCoverage(
    [oldStmt('s1', 'foo();', 9, 10)],
    [newStmt('s1', 'bar();', 10)],
  );
  assert.equal(report.inherited.length, 0);
  assert.equal(report.added.length, 1);
  assert.equal(report.uncertain.length, 1);
  assert.equal(report.uncertain[0].type, 'unmatched-old');
  const migrated = report.migratedCoverage.find((s) => s.id === 's1')!;
  assert.equal(migrated.hits, 0);
  assert.deepEqual(migrated.provenance, []);
});

test('归一化：换行与注释变化不影响指纹', () => {
  const report = migrateCoverage(
    [oldStmt('s1', 'const x = 1; // 初始化', 3)],
    [newStmt('s1', 'const   x\n  = 1;')],
  );
  assert.equal(report.inherited.length, 1);
  assert.equal(report.inherited[0].hits, 3);
});

test('指纹歧义：相同内容出现多次时不猜测，进入不确定项', () => {
  const report = migrateCoverage(
    [oldStmt('o1', 'tick();', 5, 1), oldStmt('o2', 'tick();', 0, 2)],
    [newStmt('n1', 'tick();', 1), newStmt('n2', 'tick();', 2)],
  );
  assert.equal(report.inherited.length, 0);
  assert.equal(report.added.length, 0);
  assert.equal(report.uncertain.length, 1);
  const entry = report.uncertain[0];
  assert.equal(entry.type, 'ambiguous-fingerprint');
  if (entry.type === 'ambiguous-fingerprint') {
    assert.deepEqual(entry.oldIds.sort(), ['o1', 'o2']);
    assert.deepEqual(entry.newIds.sort(), ['n1', 'n2']);
  }
});

test('rename 规则：人工确认的一对一对应可以继承', () => {
  const report = migrateCoverage(
    [oldStmt('s1', 'foo(a);', 4)],
    [newStmt('s2', 'foo(a, b);')],
    [{ kind: 'rename', oldId: 's1', newId: 's2', reason: '签名扩展，语义同一' }],
  );
  assert.equal(report.inherited.length, 1);
  assert.equal(report.inherited[0].via, 'rename-rule');
  assert.equal(report.inherited[0].hits, 4);
  assert.equal(report.uncertain.length, 0);
});

test('identity 规则指纹不一致时被拒绝，绝不强行继承', () => {
  const report = migrateCoverage(
    [oldStmt('s1', 'foo();', 4)],
    [newStmt('s2', 'bar();')],
    [{ kind: 'identity', oldId: 's1', newId: 's2' }],
  );
  assert.equal(report.inherited.length, 0);
  assert.equal(report.rejectedRules.length, 1);
  assert.equal(report.added.length, 1);
  assert.ok(report.uncertain.some((e) => e.type === 'rejected-rule'));
  assert.ok(report.uncertain.some((e) => e.type === 'unmatched-old'));
});

test('split 规则：一对多拆分，各分支共享来源并保留出处', () => {
  const report = migrateCoverage(
    [oldStmt('s1', 'doA(); doB();', 6)],
    [newStmt('n1', 'doA();'), newStmt('n2', 'doB();')],
    [{ kind: 'split', oldId: 's1', newIds: ['n1', 'n2'], reason: '复合语句拆分' }],
  );
  assert.equal(report.inherited.length, 2);
  for (const entry of report.inherited) {
    assert.equal(entry.via, 'split');
    assert.equal(entry.hits, 6);
    assert.deepEqual(entry.sources, [{ oldId: 's1', hits: 6 }]);
  }
  assert.equal(report.uncertain.length, 0);
});

test('merge 规则：多对一合并，来源分别保留，hits 取最小值', () => {
  const report = migrateCoverage(
    [oldStmt('o1', 'a();', 8), oldStmt('o2', 'b();', 3)],
    [newStmt('n1', 'a(); b();')],
    [{ kind: 'merge', oldIds: ['o1', 'o2'], newId: 'n1', reason: '合并为单行' }],
  );
  assert.equal(report.inherited.length, 1);
  const entry = report.inherited[0];
  assert.equal(entry.via, 'merge');
  assert.equal(entry.hits, 3);
  assert.deepEqual(entry.sources, [
    { oldId: 'o1', hits: 8 },
    { oldId: 'o2', hits: 3 },
  ]);
  const migrated = report.migratedCoverage.find((s) => s.id === 'n1')!;
  assert.deepEqual(migrated.provenance, entry.sources);
});

test('新增语句进入 added，旧语句消失进入 uncertain', () => {
  const report = migrateCoverage(
    [oldStmt('gone', 'oldCall();', 2)],
    [newStmt('fresh', 'newCall();')],
  );
  assert.deepEqual(report.added.map((a) => a.newId), ['fresh']);
  assert.equal(report.uncertain.length, 1);
  assert.equal(report.uncertain[0].type, 'unmatched-old');
});

test('规则引用不存在的语句时被拒绝', () => {
  const report = migrateCoverage(
    [oldStmt('s1', 'foo();', 1)],
    [newStmt('s1', 'foo();')],
    [{ kind: 'rename', oldId: 's1', newId: 'ghost', reason: '目标不存在' }],
  );
  assert.equal(report.rejectedRules.length, 1);
  // 被拒绝的规则不影响指纹自动匹配
  assert.equal(report.inherited.length, 1);
  assert.equal(report.inherited[0].via, 'auto-identical');
});

test('同一语句被多条规则占用时后者被拒绝', () => {
  const report = migrateCoverage(
    [oldStmt('o1', 'foo();', 1)],
    [newStmt('n1', 'foo();'), newStmt('n2', 'bar();')],
    [
      { kind: 'identity', oldId: 'o1', newId: 'n1' },
      { kind: 'rename', oldId: 'o1', newId: 'n2', reason: '冲突的规则' },
    ],
  );
  assert.equal(report.rejectedRules.length, 1);
  assert.equal(report.inherited.length, 1);
  assert.equal(report.inherited[0].newId, 'n1');
});
