import assert from 'node:assert/strict';
import test from 'node:test';
import { Compositor } from '../src/compositor.ts';
import { solidLayer } from './helpers.ts';

test('全透明红叠蓝不能染红（straight 与 premultiplied 两种来源）', () => {
  for (const premultiplied of [false, true]) {
    const c = new Compositor({ width: 4, height: 4, tileSize: 2 });
    c.upsertLayer(solidLayer('blue', 4, 4, [0, 0, 1, 1]));
    const ghost: [number, number, number, number] = premultiplied ? [0, 0, 0, 0] : [1, 0, 0, 0];
    c.upsertLayer(solidLayer('redGhost', 4, 4, ghost, { premultiplied }));
    const v = c.commit().version;
    for (let y = 0; y < 4; y++) {
      for (let x = 0; x < 4; x++) {
        const [r, g, b, a] = c.readPixel(v, x, y);
        assert.ok(Math.abs(r) < 1e-6, `premult=${premultiplied} red leaked: r=${r}`);
        assert.ok(Math.abs(g) < 1e-6);
        assert.ok(Math.abs(b - 1) < 1e-6, `blue damaged: b=${b}`);
        assert.ok(Math.abs(a - 1) < 1e-6);
      }
    }
  }
});

test('相同输入的 sRGB 直接混合与线性光混合明确不同', (t) => {
  const scene = (space: 'srgb' | 'linear') => {
    const c = new Compositor({ width: 2, height: 2, tileSize: 2 });
    c.upsertLayer(solidLayer('bg', 2, 2, [0, 0, 0, 1], { colorSpace: space }));
    c.upsertLayer(solidLayer('fg', 2, 2, [1, 1, 1, 0.5], { colorSpace: space }));
    return c.readPixel(c.commit().version, 0, 0)[0];
  };
  const srgb = scene('srgb');
  const linear = scene('linear');
  t.diagnostic(`50% 白叠黑: sRGB=${srgb.toFixed(4)}, linear-light=${linear.toFixed(4)}`);
  assert.ok(Math.abs(srgb - 0.5) < 1e-3, `sRGB 直接混合应为 0.5，实际 ${srgb}`);
  assert.ok(Math.abs(linear - 0.7354) < 1e-3, `线性光混合应约为 0.7354，实际 ${linear}`);
  assert.ok(Math.abs(linear - srgb) > 0.2, '两条路径必须明确不同');
});
