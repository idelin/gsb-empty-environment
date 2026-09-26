import assert from 'node:assert/strict';
import test from 'node:test';
import { Compositor } from '../src/compositor.ts';
import { solidLayer } from './helpers.ts';

test('移动半透明层：擦除旧覆盖、重算重叠层、只重绘受影响 tile', (t) => {
  const c = new Compositor({ width: 8, height: 8, tileSize: 4 }); // 2x2 共 4 tile
  c.upsertLayer(solidLayer('bg', 8, 8, [0.2, 0.2, 0.2, 1]));
  c.upsertLayer(solidLayer('red', 2, 2, [1, 0, 0, 0.5], { offsetX: 1, offsetY: 1 }));
  c.upsertLayer(solidLayer('green', 2, 2, [0, 1, 0, 0.5], { offsetX: 5, offsetY: 5 }));
  const r1 = c.commit();
  assert.equal(r1.tilesRepainted, 4, '首帧全量渲染');

  // 旧位置红叠灰 = (0.6, 0.1, 0.1)
  const before = c.readPixel(r1.version, 1, 1);
  assert.ok(Math.abs(before[0] - 0.6) < 1e-4 && Math.abs(before[1] - 0.1) < 1e-4);

  // 把红层移到 (5,5)，与绿层重叠
  c.upsertLayer(solidLayer('red', 2, 2, [1, 0, 0, 0.5], { offsetX: 5, offsetY: 5 }));
  const r2 = c.commit();
  t.diagnostic(`移动后重绘 tile: ${r2.tilesRepainted}/${r2.tilesTotal}`);
  assert.equal(r2.tilesRepainted, 2, '只重绘旧位置和新位置两个 tile');

  // 旧覆盖被擦除，恢复纯背景
  const erased = c.readPixel(r2.version, 1, 1);
  assert.ok(Math.abs(erased[0] - 0.2) < 1e-4, `旧位置未擦净: ${erased}`);
  assert.ok(Math.abs(erased[1] - 0.2) < 1e-4);

  // 新位置按插入序合成：灰底 -> 红 0.5 -> 绿 0.5 = (0.3, 0.55, 0.05)
  const over = c.readPixel(r2.version, 5, 5);
  assert.ok(Math.abs(over[0] - 0.3) < 1e-4, `重叠层未重算: ${over}`);
  assert.ok(Math.abs(over[1] - 0.55) < 1e-4);
  assert.ok(Math.abs(over[2] - 0.05) < 1e-4);

  // 旧快照仍可读：v1 里 (1,1) 仍是红色覆盖
  const snap = c.readPixel(r1.version, 1, 1);
  assert.ok(Math.abs(snap[0] - 0.6) < 1e-4, '旧快照必须保持可读');
});
