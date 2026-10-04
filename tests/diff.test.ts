import { test } from 'node:test';
import assert from 'node:assert/strict';
import { diffSnapshots, formatChain } from '../src/diff.ts';
import { computeEffective } from '../src/effective.ts';
import { compressPaths } from '../src/paths.ts';
import { snapshot, STD_CATALOG, STD_NODES } from './helpers.ts';
import type { UserResourceDiff } from '../src/types.ts';

function findUser(report: { users: UserResourceDiff[] }, userId: string, resourceType: string) {
  const entry = report.users.find((u) => u.userId === userId && u.resourceType === resourceType);
  assert.ok(entry, `缺少 ${userId}/${resourceType} 的 diff 条目`);
  return entry;
}

function actionDiff(entry: UserResourceDiff, action: string) {
  const d = entry.actions.find((a) => a.action === action);
  assert.ok(d, `缺少动作 ${action}`);
  return d;
}

test('继承权限：用户迁出组织后失去祖先授权，迁入后获得新授权', () => {
  const before = snapshot({
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
      { id: 'g-sales', orgId: 'sales', resourceType: 'folder', path: '/docs', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  });
  const after = snapshot({
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'sales' }],
    grants: [
      { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
      { id: 'g-sales', orgId: 'sales', resourceType: 'folder', path: '/docs', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  });

  const report = diffSnapshots(before, after);
  const entry = findUser(report, 'u1', 'folder');
  const read = actionDiff(entry, 'read');

  assert.deepEqual(read.added.map((p) => p.path), ['/docs/**']);
  assert.deepEqual(read.lost.map((p) => p.path), ['/code/**']);
  assert.equal(entry.hasChanges, true);

  // 新增权限的来源链：u1 -> sales -> g-sales
  assert.deepEqual(
    read.added[0].chain.map((l) => l.id),
    ['u1', 'sales', 'g-sales'],
  );
  // 失去权限的来源链：指向 before 中的来源 u1 -> backend -> eng -> g-eng
  assert.deepEqual(
    read.lost[0].chain.map((l) => l.id),
    ['u1', 'backend', 'eng', 'g-eng'],
  );
});

test('资源路径压缩：子树全覆盖折叠为 /**，部分覆盖逐条列出', () => {
  const catalog = STD_CATALOG.folder;
  assert.deepEqual(
    compressPaths(new Set(['/docs', '/docs/api', '/docs/guide']), catalog),
    ['/docs/**'],
  );
  assert.deepEqual(
    compressPaths(new Set(['/docs/api', '/docs/guide']), catalog),
    ['/docs/api', '/docs/guide'],
  );
  assert.deepEqual(
    compressPaths(new Set(['/docs', '/docs/api', '/docs/guide', '/code', '/code/src', '/code/tests']), catalog),
    ['/**'],
  );
  assert.deepEqual(compressPaths(new Set(['/code/src']), catalog), ['/code/src']);
});

test('最短来源链：同时被多级授权覆盖时取跳数最少的链', () => {
  const snap = snapshot({
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-root', orgId: 'root', resourceType: 'folder', path: '/code', actions: ['read'] },
      { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
      { id: 'g-backend', orgId: 'backend', resourceType: 'folder', path: '/code', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  });
  const eff = computeEffective(snap).get('u1')!;
  const chain = eff.byKey.get('folder read')!.get('/code/src')!;
  // 最短链：u1 -> backend -> g-backend（3 跳），而非经由 eng 或 root
  assert.deepEqual(chain.map((l) => l.id), ['u1', 'backend', 'g-backend']);
});

test('最短来源链：显式例外优先于任何继承授权', () => {
  const snap = snapshot({
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-backend', orgId: 'backend', resourceType: 'folder', path: '/code', actions: ['read'] },
    ],
    exceptions: [
      { id: 'ex1', userId: 'u1', resourceType: 'folder', path: '/code/src', effect: 'allow', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  });
  const eff = computeEffective(snap).get('u1')!;
  const chain = eff.byKey.get('folder read')!.get('/code/src')!;
  assert.deepEqual(chain.map((l) => l.id), ['u1', 'ex1']);
});

test('重命名组织不产生任何权限变化', () => {
  const spec = {
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read', 'write'] },
    ],
    catalog: STD_CATALOG,
  };
  const before = snapshot({ ...spec, nodes: STD_NODES });
  const renamed = STD_NODES.map((n) =>
    n.id === 'eng' ? { ...n, name: '研发部（原工程部）' } : n.id === 'backend' ? { ...n, name: '后端一组' } : n,
  );
  const after = snapshot({ ...spec, nodes: renamed });

  const report = diffSnapshots(before, after);
  const entry = findUser(report, 'u1', 'folder');
  assert.equal(entry.hasChanges, false);
  for (const d of entry.actions) {
    assert.deepEqual(d.added, []);
    assert.deepEqual(d.lost, []);
    assert.deepEqual(d.unchanged.map((p) => p.path), ['/code/**']);
  }
});

test('删除 allow 例外：无继承兜底时权限失去，且来源指向原例外', () => {
  const base = {
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [],
    catalog: STD_CATALOG,
  };
  const before = snapshot({
    ...base,
    exceptions: [
      { id: 'ex1', userId: 'u1', resourceType: 'folder', path: '/docs/api', effect: 'allow', actions: ['read'] },
    ],
  });
  const after = snapshot({ ...base, exceptions: [] });

  const report = diffSnapshots(before, after);
  const read = actionDiff(findUser(report, 'u1', 'folder'), 'read');
  assert.deepEqual(read.lost.map((p) => p.path), ['/docs/api']);
  assert.deepEqual(read.lost[0].chain.map((l) => l.id), ['u1', 'ex1']);
});

test('删除 allow 例外：有继承兜底时权限不变（不遗漏、不误报）', () => {
  const base = {
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  };
  const before = snapshot({
    ...base,
    exceptions: [
      { id: 'ex1', userId: 'u1', resourceType: 'folder', path: '/code/src', effect: 'allow', actions: ['read'] },
    ],
  });
  const after = snapshot({ ...base, exceptions: [] });

  const report = diffSnapshots(before, after);
  const read = actionDiff(findUser(report, 'u1', 'folder'), 'read');
  assert.deepEqual(read.added, []);
  assert.deepEqual(read.lost, []);
  assert.deepEqual(read.unchanged.map((p) => p.path), ['/code/**']);
});

test('删除 deny 例外：被遮蔽的继承权限重新显现为新增', () => {
  const base = {
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  };
  const before = snapshot({
    ...base,
    exceptions: [
      { id: 'ex-deny', userId: 'u1', resourceType: 'folder', path: '/code/tests', effect: 'deny', actions: ['read'] },
    ],
  });
  const after = snapshot({ ...base, exceptions: [] });

  const report = diffSnapshots(before, after);
  const read = actionDiff(findUser(report, 'u1', 'folder'), 'read');
  assert.deepEqual(read.added.map((p) => p.path), ['/code/tests']);
  assert.deepEqual(read.lost, []);
  // 新增来自继承授权（途经所在组织 backend）
  assert.deepEqual(read.added[0].chain.map((l) => l.id), ['u1', 'backend', 'eng', 'g-eng']);
});

test('新增 deny 例外：失去的权限来源链指向该例外', () => {
  const base = {
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  };
  const before = snapshot({ ...base, exceptions: [] });
  const after = snapshot({
    ...base,
    exceptions: [
      { id: 'ex-deny', userId: 'u1', resourceType: 'folder', path: '/code/tests', effect: 'deny', actions: ['read'] },
    ],
  });

  const report = diffSnapshots(before, after);
  const read = actionDiff(findUser(report, 'u1', 'folder'), 'read');
  assert.deepEqual(read.lost.map((p) => p.path), ['/code/tests']);
  assert.deepEqual(read.lost[0].chain.map((l) => l.id), ['u1', 'ex-deny']);
  // 其余部分不变
  assert.deepEqual(read.unchanged.map((p) => p.path), ['/code', '/code/src']);
});

test('迁移中新出现 / 消失的用户：全部权限记为新增 / 失去', () => {
  const grants = [
    { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
  ];
  const before = snapshot({
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u-old', orgId: 'backend' }],
    grants,
    catalog: STD_CATALOG,
  });
  const after = snapshot({
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u-new', orgId: 'backend' }],
    grants,
    catalog: STD_CATALOG,
  });

  const report = diffSnapshots(before, after);
  const oldEntry = findUser(report, 'u-old', 'folder');
  const newEntry = findUser(report, 'u-new', 'folder');
  assert.deepEqual(actionDiff(oldEntry, 'read').lost.map((p) => p.path), ['/code/**']);
  assert.deepEqual(actionDiff(newEntry, 'read').added.map((p) => p.path), ['/code/**']);
});

test('来源链可渲染为可读字符串', () => {
  const snap = snapshot({
    nodes: STD_NODES,
    rootId: 'root',
    users: [{ id: 'u1', orgId: 'backend' }],
    grants: [
      { id: 'g-root', orgId: 'root', resourceType: 'folder', path: '/docs', actions: ['read'] },
    ],
    catalog: STD_CATALOG,
  });
  const eff = computeEffective(snap).get('u1')!;
  const chain = eff.byKey.get('folder read')!.get('/docs')!;
  assert.equal(formatChain(chain), 'u1 -> org:backend(后端组) -> org:eng(工程部) -> org:root(总部) -> grant:g-root(g-root)');
});
