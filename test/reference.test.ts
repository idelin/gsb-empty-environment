import assert from 'node:assert/strict';
import test from 'node:test';
import { Compositor } from '../src/compositor.ts';
import { layerBounds, type Layer, type Rect } from '../src/layer.ts';
import { assertClose, mulberry32, randomLayer, referenceRender } from './helpers.ts';

// 测试侧独立计算矩形并集覆盖的 tile 数，用于校验保守脏区
function tilesFor(rects: Rect[], W: number, H: number, T: number): number {
  const tiles = new Set<number>();
  const tilesX = Math.ceil(W / T);
  const tilesY = Math.ceil(H / T);
  for (const r of rects) {
    const x0 = Math.max(0, Math.floor(r.x / T));
    const y0 = Math.max(0, Math.floor(r.y / T));
    const x1 = Math.min(tilesX - 1, Math.floor((r.x + r.w - 1) / T));
    const y1 = Math.min(tilesY - 1, Math.floor((r.y + r.h - 1) / T));
    for (let ty = y0; ty <= y1; ty++) for (let tx = x0; tx <= x1; tx++) tiles.add(ty * tilesX + tx);
  }
  return tiles.size;
}

test('随机 tile 切分 × 独立逐像素参考 × 旧快照读取 × 分配/重绘统计', (t) => {
  const rng = mulberry32(20260926);
  const TRIALS = 8;
  for (let trial = 0; trial < TRIALS; trial++) {
    const W = 5 + Math.floor(rng() * 11);
    const H = 5 + Math.floor(rng() * 11);
    const tileSize = [1, 2, 3, 4, 8][Math.floor(rng() * 5)];
    const c = new Compositor({ width: W, height: H, tileSize });
    const tilesTotal = c.tilesX * c.tilesY;

    // 场景 v1：2~5 个随机图层，30% 概率依赖前一层
    const scene: Layer[] = [];
    const nLayers = 2 + Math.floor(rng() * 4);
    for (let i = 0; i < nLayers; i++) {
      const deps = i > 0 && rng() < 0.3 ? [scene[i - 1].id] : [];
      const layer = randomLayer(rng, `L${i}`, W, H, deps);
      scene.push(layer);
      c.upsertLayer(layer);
    }
    const snapshots: { id: number; scene: Layer[] }[] = [];
    const r1 = c.commit();
    assert.equal(r1.tilesRepainted, tilesTotal, '首帧必须全量渲染');
    snapshots.push({ id: r1.version, scene: [...scene] });

    // 编辑 v2：移动一个图层（新旧包围盒都进脏区）
    const movedIdx = Math.floor(rng() * scene.length);
    const oldBounds = layerBounds(scene[movedIdx], W, H);
    const moved = { ...scene[movedIdx], offsetX: Math.floor(rng() * W), offsetY: Math.floor(rng() * H) };
    scene[movedIdx] = moved;
    c.upsertLayer(moved);
    const r2 = c.commit();
    const expectedV2 = tilesFor(
      [oldBounds, layerBounds(moved, W, H)].filter((b): b is Rect => b !== null),
      W, H, tileSize,
    );
    assert.equal(r2.tilesRepainted, expectedV2, `trial ${trial}: 重绘 tile 数必须等于保守脏区`);
    snapshots.push({ id: r2.version, scene: [...scene] });

    // 编辑 v3：删除一个图层
    const removedIdx = Math.floor(rng() * scene.length);
    const [removed] = scene.splice(removedIdx, 1);
    const removedBounds = layerBounds(removed, W, H);
    c.removeLayer(removed.id);
    const r3 = c.commit();
    const expectedV3 = tilesFor(removedBounds ? [removedBounds] : [], W, H, tileSize);
    assert.equal(r3.tilesRepainted, expectedV3, `trial ${trial}: 删除后重绘必须限于被删图层包围盒`);
    snapshots.push({ id: r3.version, scene: [...scene] });

    // 三个版本（含旧快照）都与独立逐像素参考一致
    for (const snap of snapshots) {
      const expected = referenceRender(snap.scene, W, H);
      const actual = c.snapshotPixels(snap.id);
      assertClose(actual, expected, 1e-3, `trial ${trial} version ${snap.id}`);
    }

    const s1 = c.stats();
    assert.ok(s1.allocations > 0 && s1.allocations <= tilesTotal * 3, '实际分配数受控');

    // 释放旧快照：引用计数归零后缓冲回池，后续提交应发生复用
    c.releaseVersion(r1.version);
    c.releaseVersion(r2.version);
    assert.ok(c.stats().live <= tilesTotal, `trial ${trial}: 释放后存活缓冲应不超过当前版本`);
    const layer = randomLayer(rng, 'extra', W, H, []);
    layer.offsetX = 0; // 保证可见，确保本次提交确有重绘
    layer.offsetY = 0;
    layer.crop = null;
    c.upsertLayer(layer);
    const r4 = c.commit();
    assert.ok(r4.tilesRepainted > 0);
    assert.ok(c.stats().reuses > 0, `trial ${trial}: 释放后的缓冲必须被复用`);

    t.diagnostic(
      `trial ${trial}: ${W}x${H} tile=${tileSize} layers=${nLayers} ` +
        `repaint v2=${r2.tilesRepainted}/${tilesTotal} v3=${r3.tilesRepainted}/${tilesTotal} ` +
        `alloc=${c.stats().allocations} reuse=${c.stats().reuses} live=${c.stats().live}`,
    );
  }
});
