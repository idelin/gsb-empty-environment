import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TileCache, TileStore, makePlane, rleDecode, rleEncode } from '../src/index.ts';

test('RLE round-trips arbitrary data', () => {
  const data = new Uint8Array(1000);
  let s = 7;
  for (let i = 0; i < data.length; i++) {
    s = (s * 1664525 + 1013904223) >>> 0;
    data[i] = s % 3 === 0 ? 1 : 0;
  }
  assert.deepEqual([...rleDecode(rleEncode(data), data.length)], [...data]);
  const uniform = new Uint8Array(256).fill(1);
  const enc = rleEncode(uniform);
  assert.ok(enc.length <= 4, 'uniform tile compresses to a couple of bytes');
  assert.deepEqual([...rleDecode(enc, 256)], [...uniform]);
});

test('decoded tiles are bounded by the cache capacity under pressure', () => {
  const cache = new TileCache(4);
  const store = new TileStore(64, 64, cache, 8); // 64 tiles, cap 4
  let s = 1;
  const rnd = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  // random walk over the whole plane: forces constant eviction
  for (let i = 0; i < 5000; i++) {
    const x = Math.floor(rnd() * 64);
    const y = Math.floor(rnd() * 64);
    if (rnd() < 0.5) store.set(x, y, 1);
    else store.get(x, y);
    assert.ok(cache.size <= 4, `cache exceeded capacity: ${cache.size}`);
  }
  assert.ok(cache.evictions > 0, 'expected evictions to happen');
});

test('data survives eviction cycles (cold storage is authoritative)', () => {
  const cache = new TileCache(2);
  const store = new TileStore(32, 32, cache, 8); // 16 tiles, cap 2
  const ref = makePlane(32, 32);
  let s = 99;
  const rnd = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  for (let i = 0; i < 2000; i++) {
    const x = Math.floor(rnd() * 32);
    const y = Math.floor(rnd() * 32);
    const v = rnd() < 0.4 ? 1 : 0;
    store.set(x, y, v);
    ref.data[y * 32 + x] = v;
  }
  const round = store.toPlane();
  assert.deepEqual([...round.data], [...ref.data]);
});
