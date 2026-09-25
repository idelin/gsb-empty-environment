import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ClientController } from '../src/controller.ts';
import { fixtureMetrics, fixtureTree } from '../src/fixtures.ts';
import { KeyboardAdapter } from '../src/keyboard.ts';
import { MouseAdapter } from '../src/mouse.ts';
import { Server } from '../src/server.ts';
import type { ApplyResult, MoveOp } from '../src/types.ts';

test('两个拖拽会话竞争：先提交者胜，后者因源版本过期被拒，独立编辑保留', () => {
  const server = new Server(fixtureTree());
  const c = new ClientController(fixtureTree(), server);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  const kb = new KeyboardAdapter(c);

  // 会话 M（鼠标）：拖 a1 -> inside b
  mouse.pointerDown(8, 36);
  mouse.pointerMove(20, 90);
  // 会话 K（键盘）：拖 c -> before a（两个会话同时进行）
  c.setFocus('c');
  kb.key(' ');
  kb.key('ArrowUp');
  kb.key('ArrowUp');
  kb.key('ArrowUp');
  kb.key('ArrowUp');
  kb.key('ArrowUp'); // gap 0 -> before a
  assert.equal(c.activeSessionCount(), 2);

  // M 先提交：applied，版本 0 -> 1
  mouse.pointerUp();
  assert.equal(server.tree.version, 1);
  assert.deepEqual(server.tree.nodes.b.childIds, ['b1', 'a1']);

  // K 的 op 生成后，用户做了独立编辑（重命名 a2）
  c.rename('a2', 'A2 renamed'); // 版本 1 -> 2

  // K 提交：sourceVersion=0 已过期 -> rejected，c 未被移动
  kb.key('Enter');
  assert.equal(server.tree.version, 2);
  assert.deepEqual(server.tree.rootIds, ['a', 'b', 'c']);
  assert.equal(server.tree.nodes.c.parentId, null);
  // 失败不滚回：独立编辑保留
  assert.equal(server.tree.nodes.a2.label, 'A2 renamed');
  assert.deepEqual(c.tree, server.tree);
  // 失败后焦点回到被拖节点
  assert.equal(c.focusId, 'c');
});

test('服务器重排回包使进行中的提交因版本过期被拒', () => {
  const server = new Server(fixtureTree());
  const c = new ClientController(fixtureTree(), server);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 36);   // a1
  mouse.pointerMove(20, 90);  // inside b
  // 服务器端重排根节点并回包
  c.reorder(null, ['c', 'b', 'a']);
  assert.equal(c.tree.version, 1);
  // 会话仍有效（目标 b 存在），但提交因源版本过期被拒
  mouse.pointerUp();
  assert.equal(server.tree.version, 1);
  assert.deepEqual(c.tree.rootIds, ['c', 'b', 'a']);
  assert.deepEqual(c.tree.nodes.a.childIds, ['a1', 'a2']);
});

test('操作ID幂等：响应丢失后同一 opId 重试，服务器只应用一次', () => {
  const server = new Server(fixtureTree());
  let calls = 0;
  const flakyTransport = (op: MoveOp): ApplyResult | null => {
    calls += 1;
    const result = server.applyMove(op);
    return calls === 1 ? null : result; // 第一次响应丢失
  };
  const c = new ClientController(fixtureTree(), server, flakyTransport);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 60);   // a2
  mouse.pointerMove(20, 90);  // inside b
  mouse.pointerUp();

  assert.equal(calls, 2);
  assert.equal(server.appliedOpCount(), 1);
  assert.equal(server.tree.version, 1);
  assert.deepEqual(c.tree.nodes.b.childIds, ['b1', 'a2']);
  assert.deepEqual(c.tree.nodes.a.childIds, ['a1']);
});

test('重试返回去重标记且树不重复移动', () => {
  const server = new Server(fixtureTree());
  const op: MoveOp = { opId: 'op-retry', sourceId: 'a2', targetId: 'b', placement: 'inside', sourceVersion: 0, targetRevision: 0 };
  const first = server.applyMove(op);
  assert.equal(first.status, 'applied');
  const second = server.applyMove(op);
  assert.equal(second.status, 'applied');
  assert.equal(second.deduplicated, true);
  assert.equal(server.tree.version, 1);
  assert.deepEqual(server.tree.nodes.b.childIds, ['b1', 'a2']);
});
