import assert from 'node:assert/strict';
import { mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { stripTypes } from '../scripts/strip-types.mjs';

const srcDir = new URL('../src/', import.meta.url);
const outDir = new URL('../.build/', import.meta.url);
rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });
for (const name of readdirSync(srcDir)) {
  if (!name.endsWith('.ts')) continue;
  const source = readFileSync(new URL(name, srcDir), 'utf8');
  writeFileSync(new URL(name.replace(/\.ts$/, '.js'), outDir), stripTypes(source));
}

const { checkApprovals, matchPath, findMinSubset, popcount, MAX_CANDIDATES } = await import('../.build/index.js');

let passed = 0;
const failures = [];
function test(name, fn) {
  try {
    fn();
    passed += 1;
  } catch (err) {
    failures.push([name, err]);
  }
}

const baseInput = {
  author: 'carol',
  rules: [],
  files: [],
  approvals: [],
  candidates: [],
};
function input(overrides) {
  return { ...baseInput, ...overrides };
}

// ---------- glob ----------
test('glob: 无斜杠模式匹配任意层级 basename', () => {
  assert.equal(matchPath('*.ts', 'a/b/c.ts'), true);
  assert.equal(matchPath('*.ts', 'c.ts'), true);
  assert.equal(matchPath('*.ts', 'a/b/c.js'), false);
});

test('glob: ** 跨目录匹配', () => {
  assert.equal(matchPath('src/**', 'src/a/b.ts'), true);
  assert.equal(matchPath('src/**', 'src/a.ts'), true);
  assert.equal(matchPath('src/**', 'srcx/a.ts'), false);
  assert.equal(matchPath('**', 'any/thing.md'), true);
  assert.equal(matchPath('src/**/test.ts', 'src/test.ts'), true);
  assert.equal(matchPath('src/**/test.ts', 'src/a/b/test.ts'), true);
  assert.equal(matchPath('**.md', 'x/a.md'), true);
});

test('glob: 含斜杠模式锚定根目录', () => {
  assert.equal(matchPath('/docs/*.md', 'docs/a.md'), true);
  assert.equal(matchPath('docs/*.md', 'docs/sub/a.md'), false);
  assert.equal(matchPath('docs/*.md', 'xdocs/a.md'), false);
});

test('glob: ? 与字符类与目录后缀', () => {
  assert.equal(matchPath('file?.ts', 'file1.ts'), true);
  assert.equal(matchPath('file?.ts', 'file12.ts'), false);
  assert.equal(matchPath('[abc].ts', 'b.ts'), true);
  assert.equal(matchPath('[!abc].ts', 'b.ts'), false);
  assert.equal(matchPath('docs/', 'docs/a/b.md'), true);
});

// ---------- solver ----------
test('solver: popcount', () => {
  assert.equal(popcount(0), 0);
  assert.equal(popcount(0b1011), 3);
  assert.equal(popcount(2 ** 20 - 1), 20);
});

test('solver: 最小子集与不可行', () => {
  const constraints = [
    { mask: 0b011, need: 1 },
    { mask: 0b101, need: 1 },
    { mask: 0b110, need: 1 },
  ];
  const s = findMinSubset(3, constraints);
  assert.equal(popcount(s), 2);
  assert.equal(findMinSubset(2, [{ mask: 0b01, need: 2 }]), -1);
  assert.equal(findMinSubset(0, []), 0);
});

// ---------- checker ----------
test('checker: 已有审批满足阈值，无需补充', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: ['alice', 'bob'], mode: 'override', minApprovals: 1 }],
    files: [{ path: 'src/a.ts' }],
    approvals: ['alice'],
    candidates: ['bob'],
  }));
  assert.equal(r.ok, true);
  assert.deepEqual(r.addedApprovers, []);
  assert.equal(r.files[0].satisfied, true);
});

test('checker: 作者本人审批不计入', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: ['carol', 'alice'], mode: 'override', minApprovals: 1 }],
    files: [{ path: 'a.ts' }],
    approvals: ['carol'],
    candidates: ['alice'],
  }));
  assert.equal(r.ok, true);
  assert.deepEqual(r.addedApprovers, ['alice']);
});

test('checker: override 覆盖、append 追加，按输入顺序生效', () => {
  const rules = [
    { pattern: '**', owners: ['a', 'b'], mode: 'override' },
    { pattern: 'src/**', owners: ['c'], mode: 'append' },
    { pattern: 'src/core/**', owners: ['d'], mode: 'override' },
  ];
  const r = checkApprovals(input({
    rules,
    files: [{ path: 'src/core/x.ts' }, { path: 'src/other.ts' }, { path: 'README.md' }],
    candidates: ['a', 'b', 'c', 'd'],
  }));
  const ownersOf = (p) => r.files.find((f) => f.path === p).requirements[0].owners;
  assert.deepEqual(ownersOf('src/core/x.ts'), ['d']);
  assert.deepEqual(ownersOf('src/other.ts'), ['a', 'b', 'c']);
  assert.deepEqual(ownersOf('README.md'), ['a', 'b']);
});

test('checker: 阈值取最后一个声明 minApprovals 的匹配规则', () => {
  const rules = [
    { pattern: '**', owners: ['a', 'b', 'c'], mode: 'override', minApprovals: 2 },
    { pattern: 'src/**', owners: ['d'], mode: 'append' },
  ];
  const r = checkApprovals(input({
    rules,
    files: [{ path: 'src/x.ts' }],
    candidates: ['a', 'b', 'c', 'd'],
  }));
  assert.equal(r.files[0].requirements[0].required, 2);
  assert.equal(r.addedApprovers.length, 2);
});

test('checker: 重命名文件需同时满足新旧路径规则', () => {
  const r = checkApprovals(input({
    rules: [
      { pattern: 'docs/**', owners: ['doc-owner'], mode: 'override', minApprovals: 1 },
      { pattern: 'src/**', owners: ['src-owner'], mode: 'override', minApprovals: 1 },
    ],
    files: [{ path: 'src/new.md', oldPath: 'docs/old.md' }],
    candidates: ['doc-owner', 'src-owner'],
  }));
  assert.equal(r.ok, true);
  assert.equal(r.addedApprovers.length, 2);
  assert.deepEqual(r.files[0].requirements.map((q) => q.path), ['docs/old.md', 'src/new.md']);
});

test('checker: 重命名双路径可由同一所有者一次满足', () => {
  const r = checkApprovals(input({
    rules: [
      { pattern: 'docs/**', owners: ['shared'], mode: 'override', minApprovals: 1 },
      { pattern: 'src/**', owners: ['shared'], mode: 'override', minApprovals: 1 },
    ],
    files: [{ path: 'src/new.md', oldPath: 'docs/old.md' }],
    candidates: ['shared'],
  }));
  assert.equal(r.ok, true);
  assert.deepEqual(r.addedApprovers, ['shared']);
});

test('checker: 求最少补充审批人集合', () => {
  const r = checkApprovals(input({
    rules: [
      { pattern: 'f1', owners: ['x', 'a'], mode: 'override', minApprovals: 1 },
      { pattern: 'f2', owners: ['x', 'b'], mode: 'override', minApprovals: 1 },
      { pattern: 'f3', owners: ['a', 'b'], mode: 'override', minApprovals: 1 },
    ],
    files: [{ path: 'f1' }, { path: 'f2' }, { path: 'f3' }],
    candidates: ['x', 'a', 'b'],
  }));
  assert.equal(r.ok, true);
  assert.deepEqual(r.addedApprovers, ['x', 'a']);
});

test('checker: 已有审批抵扣所需数量', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: ['a', 'b', 'c', 'd'], mode: 'override', minApprovals: 3 }],
    files: [{ path: 'x.ts' }],
    approvals: ['a', 'b'],
    candidates: ['c', 'd'],
  }));
  assert.equal(r.ok, true);
  assert.equal(r.addedApprovers.length, 1);
});

test('checker: 无可行方案时报告阻塞文件', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: ['a'], mode: 'override', minApprovals: 2 }],
    files: [{ path: 'x.ts' }],
    candidates: ['a'],
  }));
  assert.equal(r.ok, false);
  assert.equal(r.blockingFiles.length, 1);
  assert.equal(r.blockingFiles[0].path, 'x.ts');
  assert.equal(r.blockingFiles[0].shortfall, 1);
  assert.equal(r.blockingFiles[0].requiredApprovals, 2);
});

test('checker: 重命名旧路径不可满足时阻塞信息含双路径', () => {
  const r = checkApprovals(input({
    rules: [
      { pattern: 'docs/**', owners: ['gone-owner'], mode: 'override', minApprovals: 1 },
      { pattern: 'src/**', owners: ['src-owner'], mode: 'override', minApprovals: 1 },
    ],
    files: [{ path: 'src/new.md', oldPath: 'docs/old.md' }],
    candidates: ['src-owner'],
  }));
  assert.equal(r.ok, false);
  assert.equal(r.blockingFiles.length, 1);
  assert.equal(r.blockingFiles[0].path, 'src/new.md');
  assert.equal(r.blockingFiles[0].oldPath, 'docs/old.md');
  assert.equal(r.blockingFiles[0].requirementPath, 'docs/old.md');
});

test('checker: 候选超过二十名时抛错', () => {
  const candidates = Array.from({ length: MAX_CANDIDATES + 1 }, (_, i) => 'u' + i);
  assert.throws(() => checkApprovals(input({ candidates })), /at most/);
});

test('checker: 候选去重并剔除作者', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: ['a'], mode: 'override', minApprovals: 1 }],
    files: [{ path: 'x.ts' }],
    candidates: ['carol', 'a', 'a'],
  }));
  assert.equal(r.ok, true);
  assert.deepEqual(r.addedApprovers, ['a']);
});

test('checker: 无匹配规则的文件无审批要求', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: 'src/**', owners: ['a'], mode: 'override' }],
    files: [{ path: 'docs/x.md' }],
    candidates: [],
  }));
  assert.equal(r.ok, true);
  assert.deepEqual(r.addedApprovers, []);
  assert.equal(r.files[0].requirements.length, 0);
});

test('checker: minApprovals 为 0 不产生约束', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: ['a'], mode: 'override', minApprovals: 0 }],
    files: [{ path: 'x.ts' }],
  }));
  assert.equal(r.ok, true);
  assert.deepEqual(r.addedApprovers, []);
});

test('checker: defaultMinApprovals 可配置', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: ['a', 'b'], mode: 'override' }],
    files: [{ path: 'x.ts' }],
    candidates: ['a', 'b'],
    defaultMinApprovals: 2,
  }));
  assert.equal(r.ok, true);
  assert.equal(r.addedApprovers.length, 2);
});

test('checker: 多文件共享约束去重求解', () => {
  const r = checkApprovals(input({
    rules: [{ pattern: 'src/**', owners: ['a', 'b'], mode: 'override', minApprovals: 1 }],
    files: [{ path: 'src/1.ts' }, { path: 'src/2.ts' }, { path: 'src/3.ts' }],
    candidates: ['a', 'b'],
  }));
  assert.equal(r.ok, true);
  assert.equal(r.addedApprovers.length, 1);
});

test('checker: 二十名候选可求解', () => {
  const candidates = Array.from({ length: 20 }, (_, i) => 'u' + i);
  const r = checkApprovals(input({
    rules: [{ pattern: '**', owners: candidates, mode: 'override', minApprovals: 7 }],
    files: [{ path: 'x.ts' }],
    candidates,
  }));
  assert.equal(r.ok, true);
  assert.equal(r.addedApprovers.length, 7);
});

console.log(`passed ${passed}`);
if (failures.length > 0) {
  for (const [name, err] of failures) {
    console.error(`FAIL ${name}`);
    console.error(err.message);
  }
  process.exit(1);
}
console.log('all tests passed');
