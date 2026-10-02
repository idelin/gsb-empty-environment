import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SkipList } from '../src/skiplist.ts';

/** 生成 [0, n) 的随机打乱数组（Fisher–Yates）。 */
function shuffled(n: number, rand: () => number = Math.random): number[] {
  const arr = Array.from({ length: n }, (_, i) => i);
  for (let i = arr.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rand() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

/** 可复现的伪随机数发生器（mulberry32）。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function assertStrictlyIncreasing(keys: number[]): void {
  for (let i = 1; i < keys.length; i += 1) {
    assert.ok(
      keys[i] > keys[i - 1],
      `输出必须严格单调递增：keys[${i - 1}]=${keys[i - 1]}，keys[${i}]=${keys[i]}`,
    );
  }
}

test('连续随机插入 1 万个整数键值对，全量迭代严格有序且完整', () => {
  const list = new SkipList<number, string>();
  const rand = mulberry32(42);
  const keys = shuffled(10_000, rand);

  for (const key of keys) {
    list.insert(key, `value-${key}`);
  }

  assert.equal(list.size, 10_000);

  const entries = list.iterator().toArray();
  assert.equal(entries.length, 10_000);

  const outputKeys = entries.map(([k]) => k);
  assertStrictlyIncreasing(outputKeys);

  // 与排序后的基准数据逐项一致。
  const expected = [...keys].sort((a, b) => a - b);
  assert.deepEqual(outputKeys, expected);
  for (const [k, v] of entries) {
    assert.equal(v, `value-${k}`);
  }

  // 点查抽查。
  for (let i = 0; i < 10_000; i += 997) {
    assert.equal(list.get(i), `value-${i}`);
  }
});

test('任意区间迭代器：边界正确且输出严格单调递增', () => {
  const list = new SkipList<number, number>();
  const rand = mulberry32(7);
  const keys = shuffled(10_000, rand);
  for (const key of keys) {
    list.insert(key, key * 10);
  }
  const sorted = [...keys].sort((a, b) => a - b);

  // 覆盖 200 个随机区间 + 若干边界区间。
  const ranges: Array<[number, number]> = [];
  for (let i = 0; i < 200; i += 1) {
    const a = Math.floor(rand() * 10_000);
    const b = Math.floor(rand() * 10_000);
    ranges.push([Math.min(a, b), Math.max(a, b)]);
  }
  ranges.push([0, 9_999], [0, 0], [9_999, 9_999], [5_000, 5_000], [-100, 20_000]);

  for (const [lo, hi] of ranges) {
    // 默认 [start, end)。
    const halfOpen = list.range({ start: lo, end: hi }).toArray();
    const halfOpenKeys = halfOpen.map(([k]) => k);
    assertStrictlyIncreasing(halfOpenKeys);
    assert.deepEqual(
      halfOpenKeys,
      sorted.filter((k) => k >= lo && k < hi),
      `区间 [${lo}, ${hi}) 输出与基准不一致`,
    );

    // 闭区间 [start, end]。
    const closed = list.range({ start: lo, end: hi, endInclusive: true }).toArray();
    assert.deepEqual(
      closed.map(([k]) => k),
      sorted.filter((k) => k >= lo && k <= hi),
      `区间 [${lo}, ${hi}] 输出与基准不一致`,
    );

    // 开区间 (start, end)。
    const open = list
      .range({ start: lo, end: hi, startInclusive: false })
      .toArray();
    assert.deepEqual(
      open.map(([k]) => k),
      sorted.filter((k) => k > lo && k < hi),
      `区间 (${lo}, ${hi}) 输出与基准不一致`,
    );
  }
});

test('迭代遍历过程中持续插入新数据，迭代器输出完全稳定隔离', () => {
  const list = new SkipList<number, string>();
  const rand = mulberry32(2024);

  // 初始数据：偶数键 0..9998。
  const initialKeys = shuffled(5_000, rand).map((i) => i * 2);
  for (const key of initialKeys) {
    list.insert(key, `initial-${key}`);
  }
  const snapshotExpected = list.iterator().toArray();

  // 创建快照迭代器后，持续向跳表写入（交错在遍历过程中）。
  const iterator = list.iterator();

  const collected: Array<[number, string]> = [];
  const newKeys = shuffled(5_000, rand).map((i) => i * 2 + 1); // 奇数新键，穿插在快照键之间
  let inserted = 0;

  for (const entry of iterator) {
    collected.push(entry);
    // 每读 10 条快照数据，插入 50 条新数据，模拟并发写入压力。
    if (collected.length % 10 === 0 && inserted < newKeys.length) {
      for (let n = 0; n < 50 && inserted < newKeys.length; n += 1) {
        list.insert(newKeys[inserted], `late-${newKeys[inserted]}`);
        inserted += 1;
      }
    }
  }
  // 遍历结束后把剩余新键全部写入。
  while (inserted < newKeys.length) {
    list.insert(newKeys[inserted], `late-${newKeys[inserted]}`);
    inserted += 1;
  }

  // 1) 迭代器输出与创建快照时的全量数据完全一致（稳定隔离）。
  assert.deepEqual(collected, snapshotExpected);
  assert.equal(collected.length, 5_000);
  assert.ok(collected.every(([k]) => k % 2 === 0), '迭代器不应看到任何遍历期间插入的奇数键');

  // 2) 跳表本身已包含全部新数据，新建迭代器可见。
  assert.equal(list.size, 10_000);
  const fresh = list.iterator().toArray();
  assert.equal(fresh.length, 10_000);
  assertStrictlyIncreasing(fresh.map(([k]) => k));
});

test('并发交错（异步任务）写入期间，多个快照迭代器各自隔离', async () => {
  const list = new SkipList<number, number>();
  for (let i = 0; i < 2_000; i += 1) {
    list.insert(i, i);
  }

  // 在三个确定的快照点创建迭代器：2 000 条、3 000 条、4 000 条。
  const iter1 = list.iterator();
  for (let i = 0; i < 1_000; i += 1) list.insert(2_000 + i, i);
  const iter2 = list.iterator();
  for (let i = 0; i < 1_000; i += 1) list.insert(3_000 + i, i);
  const iter3 = list.iterator();
  const expectedSizes = [2_000, 3_000, 4_000];

  // 后台写入任务：在三个迭代器遍历期间持续插入更多数据。
  let stopWriting = false;
  const writer = (async () => {
    let key = 4_000;
    while (!stopWriting) {
      for (let n = 0; n < 100; n += 1) {
        list.insert(key, key);
        key += 1;
      }
      await new Promise((r) => setImmediate(r)); // 让出事件循环，模拟并发交错
    }
    return key - 4_000;
  })();

  // 三个迭代器并发遍历，遍历中频繁让出以与写入任务交错。
  const drain = async (iter: IterableIterator<[number, number]>) => {
    const out: Array<[number, number]> = [];
    for (const entry of iter) {
      out.push(entry);
      if (out.length % 50 === 0) {
        await new Promise((r) => setImmediate(r));
      }
    }
    return out;
  };
  const snapshots = await Promise.all([drain(iter1), drain(iter2), drain(iter3)]);
  stopWriting = true;
  const extraWritten = await writer;

  assert.ok(extraWritten > 0, '遍历期间应有持续写入发生');
  for (let s = 0; s < snapshots.length; s += 1) {
    const keys = snapshots[s].map(([k]) => k);
    assertStrictlyIncreasing(keys);
    assert.deepEqual(
      keys,
      Array.from({ length: expectedSizes[s] }, (_, i) => i),
      `第 ${s + 1} 个快照内容应与创建时刻的数据完全一致（稳定隔离）`,
    );
  }
});

test('同一键的并发更新对快照迭代器隔离（MVCC 版本链）', () => {
  const list = new SkipList<number, string>();
  for (let i = 0; i < 1_000; i += 1) {
    list.insert(i, `v1-${i}`);
  }

  const iter = list.iterator();

  // 快照建立后更新全部键。
  for (let i = 0; i < 1_000; i += 1) {
    list.insert(i, `v2-${i}`);
  }

  const fromSnapshot = iter.toArray();
  assert.equal(fromSnapshot.length, 1_000);
  for (const [k, v] of fromSnapshot) {
    assert.equal(v, `v1-${k}`, '快照迭代器必须读到旧版本值');
  }

  // 新迭代器读到最新版本。
  const fresh = list.iterator().toArray();
  for (const [k, v] of fresh) {
    assert.equal(v, `v2-${k}`);
  }
  assert.equal(list.size, 1_000, '重复键更新不应改变元素个数');
});

test('空表与单元素边界', () => {
  const list = new SkipList<number, string>();
  assert.equal(list.iterator().toArray().length, 0);
  assert.equal(list.range({ start: 0, end: 100 }).toArray().length, 0);

  list.insert(42, 'answer');
  assert.deepEqual(list.iterator().toArray(), [[42, 'answer']]);
  assert.deepEqual(list.range({ start: 42, end: 42 }).toArray(), []);
  assert.deepEqual(list.range({ start: 42, end: 42, endInclusive: true }).toArray(), [[42, 'answer']]);
  assert.deepEqual(list.range({ start: 42, startInclusive: false }).toArray(), []);
});
