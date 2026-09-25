import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ClientController } from '../src/controller.ts';
import { fixtureMetrics, fixtureTree } from '../src/fixtures.ts';
import { KeyboardAdapter } from '../src/keyboard.ts';
import { MouseAdapter } from '../src/mouse.ts';
import { Server } from '../src/server.ts';
import type { MoveSpec } from '../src/types.ts';

// 行高 24：a:[0,24) a1:[24,48) a2:[48,72) b:[72,96) b1:[96,120) c:[120,144)

function mouseRun(): ClientController {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 60);   // 按在 a2 行
  mouse.pointerMove(8, 78);   // b 行上半 -> gap 3, depth 0 -> before b
  mouse.pointerMove(20, 90);  // b 行下半 -> gap 4, depth 1 -> inside b
  mouse.pointerUp();
  return c;
}

function keyboardRun(): ClientController {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const kb = new KeyboardAdapter(c);
  c.setFocus('a2');
  kb.key(' ');          // 抬起 a2（gap=2, depth=1）
  kb.key('ArrowDown');  // gap 3 -> before b
  kb.key('ArrowDown');  // gap 4 -> inside b
  kb.key('Enter');
  return c;
}

test('鼠标与键盘生成完全相同的逻辑操作轨迹', () => {
  const mouse = mouseRun();
  const keyboard = keyboardRun();
  assert.deepEqual(keyboard.trace, mouse.trace);
});

test('鼠标与键盘最终树、焦点、选择一致', () => {
  const mouse = mouseRun();
  const keyboard = keyboardRun();
  assert.deepEqual(keyboard.tree, mouse.tree);
  assert.equal(keyboard.focusId, mouse.focusId);
  assert.equal(mouse.focusId, 'a2');
  assert.deepEqual([...keyboard.selection], [...mouse.selection]);
  assert.deepEqual(mouse.tree.nodes.b.childIds, ['b1', 'a2']);
});

function mouseCancelRun(): ClientController {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  c.setFocus('a2');
  c.select(['a', 'b1']);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 60);
  mouse.pointerMove(8, 78);
  mouse.pointerMove(20, 90);
  mouse.cancel();
  return c;
}

function keyboardCancelRun(): ClientController {
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  c.setFocus('a2');
  c.select(['a', 'b1']);
  const kb = new KeyboardAdapter(c);
  kb.key(' ');
  kb.key('ArrowDown');
  kb.key('ArrowDown');
  kb.key('Escape');
  return c;
}

test('鼠标与键盘取消结果一致：轨迹、树、焦点、选择全部恢复', () => {
  const mouse = mouseCancelRun();
  const keyboard = keyboardCancelRun();
  assert.deepEqual(keyboard.trace, mouse.trace);
  assert.equal(mouse.trace[mouse.trace.length - 1].kind, 'cancel');
  assert.deepEqual(mouse.tree, fixtureTree());
  assert.deepEqual(keyboard.tree, mouse.tree);
  assert.equal(mouse.focusId, 'a2');
  assert.equal(keyboard.focusId, 'a2');
  assert.deepEqual([...mouse.selection].sort(), ['a', 'b1']);
  assert.deepEqual([...keyboard.selection].sort(), ['a', 'b1']);
});

function patchOf(c: ClientController): MoveSpec {
  const commit = c.trace.find((e) => e.kind === 'commit');
  assert.ok(commit && commit.kind === 'commit');
  const { sourceId, targetId, placement } = commit.op;
  return { sourceId, targetId, placement };
}

test('键盘 [ 提升层级与鼠标拖到同位置产生同一补丁', () => {
  // 键盘：抬起 a1，[ 提升层级，再下移插入线到 b 之前
  const kbc = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const kb = new KeyboardAdapter(kbc);
  kbc.setFocus('a1');
  kb.key(' ');
  kb.key('[');          // depth 1 -> 0（提升到根层级）
  kb.key('ArrowDown');  // gap 2
  kb.key('ArrowDown');  // gap 3 -> before b
  kb.key('Enter');

  // 鼠标：拖 a1 到 b 行上半、根层级 x 处
  const mc = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const mouse = new MouseAdapter(mc, fixtureMetrics);
  mouse.pointerDown(8, 36);  // a1 行
  mouse.pointerMove(8, 66);  // a2 行下半 -> gap 3, depth 0 -> before b
  mouse.pointerUp();

  assert.deepEqual(patchOf(kbc), { sourceId: 'a1', targetId: 'b', placement: 'before' });
  assert.deepEqual(patchOf(kbc), patchOf(mc));
  assert.deepEqual(kbc.tree, mc.tree);
  assert.deepEqual(kbc.tree.rootIds, ['a', 'a1', 'b', 'c']);
});
