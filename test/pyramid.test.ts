import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createImage, getPixel, referencePyramid, type RGBAImage } from '../src/image.ts';
import { TilePyramid, SnapshotExpiredError } from '../src/pyramid.ts';

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 2 ** 32;
  };
}

function randomImage(w: number, h: number, seed: number): RGBAImage {
  const rnd = lcg(seed);
  return createImage(w, h, () => [
    Math.floor(rnd() * 256),
    Math.floor(rnd() * 256),
    Math.floor(rnd() * 256),
    Math.floor(rnd() * 256),
  ]);
}

function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

async function readFull(pyr: TilePyramid, level: number) {
  return pyr.readRegion(level, 0, 0, pyr.levelWidth(level), pyr.levelHeight(level));
}

test('层尺寸按 ceil(n / 2^L) 取整（3x3 与 7x5）', () => {
  const p3 = new TilePyramid(createImage(3, 3));
  assert.deepEqual(
    p3.levels.map((l) => [l.width, l.height]),
    [[3, 3], [2, 2], [1, 1]],
  );
  const p75 = new TilePyramid(createImage(7, 5));
  assert.deepEqual(
    p75.levels.map((l) => [l.width, l.height]),
    [[7, 5], [4, 3], [2, 2], [1, 1]],
  );
});

test('奇数尺寸边缘权重：纯白图各层不变暗', async () => {
  for (const [w, h] of [[3, 3], [5, 1], [1, 5], [9, 7]] as const) {
    const pyr = new TilePyramid(createImage(w, h, () => [255, 255, 255, 255]));
    for (let l = 0; l < pyr.levelCount(); l++) {
      const r = await readFull(pyr, l);
      assert.ok(r.data.every((v) => v === 255), `${w}x${h} L${l} 出现变暗`);
    }
  }
});

test('奇数尺寸边缘权重：单点亮度按实际覆盖像素加权', async () => {
  // 3x3 仅 (2,0) 为 255：L1(1,0) 覆盖 {(2,0),(2,1)} 两个像素 -> 128（补零会得到 64）
  const img = createImage(3, 3);
  const pyr = new TilePyramid(img);
  pyr.setPixel(2, 0, 255, 255, 255, 255);
  const l1 = await readFull(pyr, 1);
  assert.equal(l1.data[(0 * 2 + 1) * 4], 128);
  // L2(0,0) 覆盖 L1 全图 {0,128,0,0} -> 32
  const l2 = await readFull(pyr, 2);
  assert.equal(l2.data[0], 32);
});

test('瓦片管线与独立逐像素参考实现一致（奇数尺寸多层传播）', async () => {
  const sizes: [number, number][] = [[3, 3], [5, 5], [7, 5], [9, 7], [13, 11], [16, 16]];
  for (const tileSize of [4, 3, 2]) {
    for (const [w, h] of sizes) {
      const img = randomImage(w, h, w * 1000 + h * 10 + tileSize);
      const pyr = new TilePyramid(img, { tileSize });
      const ref = referencePyramid(img);
      assert.equal(pyr.levelCount(), ref.length, `${w}x${h} 层数`);
      for (let l = 0; l < ref.length; l++) {
        const r = await readFull(pyr, l);
        assert.equal(r.width, ref[l].width);
        assert.equal(r.height, ref[l].height);
        assert.deepEqual(
          [...r.data],
          [...ref[l].data],
          `${w}x${h} tileSize=${tileSize} L${l} 与参考不一致`,
        );
      }
    }
  }
});

test('局部编辑只失效祖先链，不相关分支不重建', async () => {
  const img = randomImage(16, 16, 42);
  const computeCounts = new Map<string, number>();
  const pyr = new TilePyramid(img, {
    tileSize: 4,
    hooks: {
      onCompute: (key) => computeCounts.set(key, (computeCounts.get(key) ?? 0) + 1),
    },
  });
  // 预热所有层
  for (let l = 0; l < pyr.levelCount(); l++) await readFull(pyr, l);
  const gensBefore = new Map<string, number[]>();
  for (const key of pyr.debugCachedKeys()) gensBefore.set(key, pyr.debugVersionGens(key));

  computeCounts.clear();
  const invalidated = pyr.setPixel(1, 2, 9, 9, 9, 255);
  assert.deepEqual(invalidated, ['0:0:0', '1:0:0', '2:0:0', '3:0:0', '4:0:0']);

  // 重新读取全部层：只有祖先链上的 5 个瓦片被重算
  for (let l = 0; l < pyr.levelCount(); l++) await readFull(pyr, l);
  assert.deepEqual([...computeCounts.keys()].sort(), ['0:0:0', '1:0:0', '2:0:0', '3:0:0', '4:0:0']);
  for (const n of computeCounts.values()) assert.equal(n, 1);

  // 不相关分支的版本完全未动（仍是旧代际数据，直接命中）
  assert.deepEqual(pyr.debugVersionGens('0:1:1'), gensBefore.get('0:1:1'));
  assert.deepEqual(pyr.debugVersionGens('0:3:3'), gensBefore.get('0:3:3'));
  assert.deepEqual(pyr.debugVersionGens('1:1:1'), gensBefore.get('1:1:1'));

  // 新数据与参考一致
  const ref = referencePyramid(img);
  for (let l = 0; l < ref.length; l++) {
    const r = await readFull(pyr, l);
    assert.deepEqual([...r.data], [...ref[l].data], `编辑后 L${l} 与参考不一致`);
  }
});

test('读快照与编辑失效原子隔离：进行中的读看到旧像素', async () => {
  const img = createImage(8, 4, (x, y) => [x * 10, y * 20, 0, 255]);
  const gate = deferred();
  const pyr = new TilePyramid(img, {
    tileSize: 4,
    hooks: { gate: (key) => (key === '0:1:0' ? gate.promise : undefined) },
  });
  // 预热左侧瓦片，使其旧版本进入缓存
  await pyr.getTile(0, 0, 0);

  const readPromise = pyr.readRegion(0, 0, 0, 8, 4);
  // 读在 '0:1:0' 的栅栏处等待期间发生编辑
  await new Promise((r) => setImmediate(r));
  pyr.setPixel(0, 0, 255, 0, 0, 255);
  gate.resolve();

  const snapshot = await readPromise;
  assert.deepEqual([...snapshot.data.slice(0, 4)], [0, 0, 0, 255], '快照读必须看到编辑前的像素');

  const fresh = await pyr.readRegion(0, 0, 0, 8, 4);
  assert.deepEqual([...fresh.data.slice(0, 4)], [255, 0, 0, 255], '新读必须看到编辑后的像素');
});

test('固定快照在旧版本不可得时抛 SnapshotExpiredError', async () => {
  const img = randomImage(8, 4, 7);
  const pyr = new TilePyramid(img, { tileSize: 4 });
  const gen = pyr.generation;
  pyr.setPixel(5, 1, 1, 2, 3, 255); // 失效 0:1:0，且该瓦片从未被缓存
  await assert.rejects(pyr.getTile(0, 1, 0, { snapshotGen: gen }), SnapshotExpiredError);
});

test('并发生成同一瓦片只计算并发布一次', async () => {
  const img = randomImage(8, 8, 11);
  const computeCounts = new Map<string, number>();
  const pyr = new TilePyramid(img, {
    tileSize: 4,
    hooks: { onCompute: (key) => computeCounts.set(key, (computeCounts.get(key) ?? 0) + 1) },
  });
  const results = await Promise.all([
    pyr.getTile(1, 0, 0),
    pyr.getTile(1, 0, 0),
    pyr.getTile(1, 0, 0),
    pyr.getTile(1, 0, 0),
  ]);
  assert.equal(computeCounts.get('1:0:0'), 1, '同一瓦片并发请求只应计算一次');
  assert.equal(pyr.stats.computes, 5); // 1 个 L1 瓦片 + 4 个 L0 父瓦片
  for (const r of results) assert.equal(r, results[0], '所有等待者共享同一缓冲');
});

test('旧代际任务不能复活旧像素', async () => {
  const img = createImage(8, 8, () => [10, 10, 10, 255]);
  const gate = deferred();
  let gateUsed = false;
  const pyr = new TilePyramid(img, {
    tileSize: 4,
    hooks: {
      gate: (key) => {
        if (key === '1:0:0' && !gateUsed) {
          gateUsed = true;
          return gate.promise;
        }
        return undefined;
      },
    },
  });

  const p = pyr.getTile(1, 0, 0);
  await new Promise((r) => setImmediate(r)); // 任务在栅栏处等待
  pyr.setPixel(0, 0, 250, 0, 0, 255); // 计算期间失效该瓦片
  gate.resolve();

  const data = await p; // 旧任务结果被丢弃，自动按新代际重算
  // L1(0,0) = base {(0,0),(1,0),(0,1),(1,1)} 的均值 = (250+10+10+10)/4 = 70
  assert.equal(data[0], 70, '必须拿到编辑后的新像素');
  // 版本链：墓碑 gen1 之后只有 gen1 的数据版本，不存在 gen0 的旧数据
  assert.deepEqual(pyr.debugVersionGens('1:0:0'), [1, 1]);
  const ref = referencePyramid(img);
  const l1 = await readFull(pyr, 1);
  assert.deepEqual([...l1.data], [...ref[1].data]);
});

test('缓存额度：LRU 淘汰、峰值统计', async () => {
  // 24x4，tileSize 4 -> 6 个 L0 瓦片，每个 4*4*4 = 64 字节；额度 3 个瓦片
  const img = randomImage(24, 4, 99);
  const pyr = new TilePyramid(img, { tileSize: 4, maxCacheBytes: 192 });
  for (let tx = 0; tx < 6; tx++) await pyr.getTile(0, tx, 0);

  const stats = pyr.statsSnapshot();
  assert.ok(stats.currentBytes <= 192, `静息缓存 ${stats.currentBytes} 超过额度`);
  assert.equal(stats.currentBytes, 192);
  assert.equal(stats.peakBytes, 256, '峰值应记录到发布瞬间的 4 瓦片 256 字节');
  assert.ok(stats.evictions >= 3, `evictions=${stats.evictions}`);
  assert.deepEqual(pyr.debugCachedKeys(), ['0:3:0', '0:4:0', '0:5:0']);
});

test('缓存 LRU：最近访问的瓦片不被淘汰', async () => {
  const img = randomImage(24, 4, 100);
  const pyr = new TilePyramid(img, { tileSize: 4, maxCacheBytes: 192 });
  await pyr.getTile(0, 0, 0);
  await pyr.getTile(0, 1, 0);
  await pyr.getTile(0, 2, 0);
  await pyr.getTile(0, 0, 0); // 触碰 0，使 1 成为最旧
  await pyr.getTile(0, 3, 0); // 触发淘汰 -> 应淘汰 1
  assert.deepEqual(pyr.debugCachedKeys(), ['0:0:0', '0:2:0', '0:3:0']);
});

test('取消：所有等待者取消后任务中止、缓冲释放、统计入账', async () => {
  const img = randomImage(8, 8, 55);
  const gate = deferred();
  const pyr = new TilePyramid(img, { tileSize: 4, hooks: { gate: () => gate.promise } });

  const ac = new AbortController();
  const p = pyr.getTile(0, 0, 0, { signal: ac.signal });
  await new Promise((r) => setImmediate(r)); // 任务已分配瓦片缓冲并在栅栏等待
  ac.abort();
  gate.resolve();

  await assert.rejects(p, /aborted/);
  const stats = pyr.statsSnapshot();
  assert.equal(stats.cancels, 1);
  assert.equal(stats.releasedBytes, 64, '取消应释放已分配的 4x4x4 瓦片缓冲');
  assert.equal(stats.currentBytes, 0);
  assert.deepEqual(pyr.debugCachedKeys(), []);

  // 取消后同一瓦片可重新正常生成
  const data = await pyr.getTile(0, 0, 0);
  assert.equal(data.length, 64);
  assert.equal(pyr.statsSnapshot().computes, 1);
});

test('任意区域读：跨瓦片精确边缘覆盖 + 越界裁剪', async () => {
  const img = randomImage(13, 11, 1234);
  const pyr = new TilePyramid(img, { tileSize: 4 });
  const ref = referencePyramid(img);

  // 跨 4 个瓦片的内部区域（L0）
  const r0 = await pyr.readRegion(0, 5, 3, 7, 6);
  assert.equal(r0.width, 7);
  assert.equal(r0.height, 6);
  for (let y = 0; y < 6; y++) {
    for (let x = 0; x < 7; x++) {
      const o = (y * 7 + x) * 4;
      const expected = getPixel(ref[0], 5 + x, 3 + y);
      assert.deepEqual([...r0.data.slice(o, o + 4)], [...expected], `L0 (${x},${y})`);
    }
  }

  // L1（7x6）跨瓦片区域
  const r1 = await pyr.readRegion(1, 2, 1, 4, 4);
  for (let y = 0; y < 4; y++) {
    for (let x = 0; x < 4; x++) {
      const o = (y * 4 + x) * 4;
      const expected = getPixel(ref[1], 2 + x, 1 + y);
      assert.deepEqual([...r1.data.slice(o, o + 4)], [...expected], `L1 (${x},${y})`);
    }
  }

  // 越界矩形裁剪到图像边缘（13x11 中从 (10,8) 起 10x10 -> 3x3）
  const rc = await pyr.readRegion(0, 10, 8, 10, 10);
  assert.equal(rc.width, 3);
  assert.equal(rc.height, 3);
  for (let y = 0; y < 3; y++) {
    for (let x = 0; x < 3; x++) {
      const o = (y * 3 + x) * 4;
      const expected = getPixel(ref[0], 10 + x, 8 + y);
      assert.deepEqual([...rc.data.slice(o, o + 4)], [...expected], `clip (${x},${y})`);
    }
  }
});

test('跨层选择：readScaled 选取分辨率足够的最粗层', async () => {
  const img = randomImage(16, 16, 777);
  const pyr = new TilePyramid(img, { tileSize: 4 });
  const ref = referencePyramid(img);

  // 输出 4x4：应选 L2（4x4），最近邻 1:1，像素与参考完全一致
  const r = await pyr.readScaled(0, 0, 16, 16, 4, 4);
  assert.equal(r.level, 2);
  assert.deepEqual([...r.data], [...ref[2].data]);

  // 输出 3x3：16x16 的层为 16/8/4/2/1，分辨率 >= 3 的最粗层是 L2（4x4）
  const r3 = await pyr.readScaled(0, 0, 16, 16, 3, 3);
  assert.equal(r3.level, 2);
  assert.equal(r3.width, 3);
  assert.equal(r3.height, 3);
});
