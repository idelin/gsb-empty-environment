import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Stats } from '../src/stats.ts';
import { TileStore } from '../src/store.ts';

test('TileStore 溢写/回读/释放/销毁', () => {
  const dir = mkdtempSync(join(tmpdir(), 'store-'));
  const stats = new Stats();
  const tileBytes = 2 * 2 * 16;
  const store = new TileStore(dir, tileBytes * 2, tileBytes, stats);
  const t = (v: number) => new Float32Array(16).fill(v);

  store.put('s:0,0', t(1));
  store.put('s:0,1', t(2));
  store.put('s:1,0', t(3)); // 预算仅 2 个 tile -> 触发溢写
  assert.equal(stats.spillCount, 1);
  assert.equal(readdirSync(dir).length, 1);

  const back = store.get('s:0,0');
  assert.ok(back);
  assert.deepEqual(Array.from(back), new Array(16).fill(1));
  assert.equal(stats.loadCount, 1);
  assert.ok(stats.peakBytes > 0);

  store.releaseAll();
  assert.equal(readdirSync(dir).length, 0, '临时文件全部释放');
  assert.equal(stats.cacheBytes, 0);

  store.dispose();
  assert.equal(existsSync(dir), false);
});
