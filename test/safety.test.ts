import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ClientController } from '../src/controller.ts';
import { fixtureMetrics, fixtureTree } from '../src/fixtures.ts';
import { KeyboardAdapter } from '../src/keyboard.ts';
import { MouseAdapter } from '../src/mouse.ts';
import { Server } from '../src/server.ts';

test('预览不修改正式树（引用与内容均不变）', () => {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const canonicalRef = c.tree;
  const canonicalSnapshot = structuredClone(c.tree);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 60);
  mouse.pointerMove(20, 90); // 预览 a2 -> inside b
  assert.equal(c.tree, canonicalRef);
  assert.deepEqual(c.tree, canonicalSnapshot);
  // 预览行反映了移动，但正式树没有
  assert.deepEqual(
    c.previewRows().map((r) => `${r.id}@${r.depth}`),
    ['a@0', 'a1@1', 'b@0', 'b1@1', 'a2@1', 'c@0'],
  );
  assert.deepEqual(c.rows().map((r) => r.id), ['a', 'a1', 'a2', 'b', 'b1', 'c']);
  mouse.cancel();
  assert.equal(c.tree, canonicalRef);
});

test('取消不修改列表，恢复全部状态', () => {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  c.setFocus('b1');
  c.select(['a1', 'b1']);
  const before = structuredClone(c.tree);
  const kb = new KeyboardAdapter(c);
  c.setFocus('a2');
  // 注意：beginSession 快照在 Space 时生成，先恢复焦点语义
  c.setFocus('b1');
  c.select(['a1', 'b1']);
  kb.key('ArrowUp'); // 焦点 b1 -> b
  kb.key('ArrowUp'); // 焦点 b -> a2
  kb.key(' ');       // 抬起 a2（快照：focus=a2, selection=[a1,b1]）
  kb.key('ArrowDown');
  kb.key('ArrowDown');
  kb.key('Escape');
  assert.deepEqual(c.tree, before);
  assert.equal(c.focusId, 'a2');
  assert.deepEqual([...c.selection].sort(), ['a1', 'b1']);
});

test('禁止移入自身后代：悬停被拦截，提交为空操作', () => {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 12);   // 按在 a 行
  mouse.pointerMove(40, 60);  // gap 2, depth 2 -> inside a1（a 的后代）
  mouse.pointerUp();
  assert.deepEqual(c.tree, fixtureTree());
  assert.equal(c.tree.version, 0);
  assert.equal(c.trace.filter((e) => e.kind === 'commit').length, 0);
});

test('目标在拖动中被删除：会话安全终止，后续提交为空操作', () => {
  const server = new Server(fixtureTree());
  const c = new ClientController(fixtureTree(), server);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 36);   // a1
  mouse.pointerMove(20, 90);  // 预览 inside b
  assert.equal(c.activeSessionCount(), 1);
  // 服务器删除 b 子树并回包
  c.remove('b');
  assert.equal(c.activeSessionCount(), 0);
  assert.ok(c.trace.some((e) => e.kind === 'terminate' && e.reason === 'target-removed'));
  // 随后的 pointerUp 安全地什么都不做
  mouse.pointerUp();
  assert.equal(c.trace.filter((e) => e.kind === 'commit').length, 0);
  assert.deepEqual(c.rows().map((r) => r.id), ['a', 'a1', 'a2', 'c']);
  assert.ok(c.focusId === null || c.tree.nodes[c.focusId] !== undefined);
});

test('源节点在拖动中被删除：会话安全终止', () => {
  const server = new Server(fixtureTree());
  const c = new ClientController(fixtureTree(), server);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 60);   // a2
  mouse.pointerMove(8, 78);
  c.remove('a2');
  assert.equal(c.activeSessionCount(), 0);
  assert.ok(c.trace.some((e) => e.kind === 'terminate' && e.reason === 'source-removed'));
  mouse.pointerUp();
  assert.deepEqual(c.rows().map((r) => r.id), ['a', 'a1', 'b', 'b1', 'c']);
});

test('键盘取消与提交后状态机回到可复用状态', () => {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const kb = new KeyboardAdapter(c);
  c.setFocus('a2');
  kb.key(' ');
  kb.key('Escape');
  kb.key(' ');          // 再次抬起同一节点
  kb.key('ArrowDown');
  kb.key('ArrowDown');
  kb.key('Enter');      // 提交 a2 -> inside b
  assert.deepEqual(c.tree.nodes.b.childIds, ['b1', 'a2']);
  assert.equal(c.focusId, 'a2');
});
