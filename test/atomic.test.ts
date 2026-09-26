import assert from 'node:assert/strict';
import test from 'node:test';
import { Compositor } from '../src/compositor.ts';
import { solidLayer } from './helpers.ts';

test('颜色转换中途失败不发布半幅图，缓冲无泄漏，可恢复', (t) => {
  const c = new Compositor({ width: 8, height: 4, tileSize: 4 }); // 2 个 tile
  c.upsertLayer(solidLayer('bg', 8, 4, [0, 0, 1, 1]));
  const v1 = c.commit().version;
  const liveBefore = c.stats().live;

  // 合法图层弄脏 tile 0（会先成功渲染），坏图层弄脏 tile 1（渲染中途抛错）
  c.upsertLayer(solidLayer('ok', 2, 2, [1, 0, 0, 1], { offsetX: 0, offsetY: 0 }));
  const bad = solidLayer('bad', 4, 4, [1, 1, 1, 1], { offsetX: 4, offsetY: 0 });
  (bad as { colorSpace: string }).colorSpace = 'cmyk';
  c.upsertLayer(bad);

  assert.throws(() => c.commit(), /unknown color space/);
  assert.equal(c.currentVersion, v1, '失败后当前版本不得前进');
  assert.equal(c.stats().live, liveBefore, '已分配瓦片必须全部归还，无泄漏');

  // 旧快照未被半幅污染：tile 0 仍是蓝色而不是已渲染的红色
  const p00 = c.readPixel(v1, 0, 0);
  assert.ok(Math.abs(p00[2] - 1) < 1e-6 && Math.abs(p00[0]) < 1e-6, '旧版本被部分写入污染');
  const p60 = c.readPixel(v1, 6, 2);
  assert.ok(Math.abs(p60[2] - 1) < 1e-6);

  // 移除坏图层后可正常恢复提交
  c.removeLayer('bad');
  const r = c.commit();
  t.diagnostic(`恢复提交重绘 tile: ${r.tilesRepainted}/${r.tilesTotal}`);
  const after = c.readPixel(r.version, 0, 0);
  assert.ok(Math.abs(after[0] - 1) < 1e-6, '恢复后红色图层应生效');
});
