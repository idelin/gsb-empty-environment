import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  allocate,
  compare,
  bound,
  StrategyTable,
  type Identifier,
} from '../src/lseq.ts';
import { LseqDocument, type Op, mulberry32 } from '../src/document.ts';

/** 确定性洗牌。 */
function shuffle<T>(arr: T[], random: () => number): T[] {
  const a = arr.slice();
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1));
    [a[i], a[j]] = [a[j], a[i]];
  }
  return a;
}

test('allocate 生成的标识符严格位于 prev 与 next 之间', () => {
  const random = mulberry32(42);
  const strategies = new StrategyTable(random);
  const ids: Identifier[] = [];
  // 模拟 2000 次随机位置插入，验证全序始终一致
  for (let i = 0; i < 2000; i++) {
    const index = Math.floor(random() * (ids.length + 1));
    const prev = index > 0 ? ids[index - 1] : null;
    const next = index < ids.length ? ids[index] : null;
    const id = allocate(prev, next, 1, strategies, random);
    if (prev) assert.ok(compare(prev, id) < 0, 'prev < id');
    if (next) assert.ok(compare(id, next) < 0, 'id < next');
    ids.splice(index, 0, id);
    // 数组保持严格升序
    for (let k = 1; k < ids.length; k++) {
      assert.ok(compare(ids[k - 1], ids[k]) < 0);
    }
  }
});

test('紧邻边界（digit 0 下探标记）之间仍可分配', () => {
  const random = mulberry32(7);
  const strategies = new StrategyTable(random);
  // 反复在最前端插入，强制产生 digit 0 下探标记与紧邻边界
  let next: Identifier | null = null;
  for (let i = 0; i < 500; i++) {
    const id = allocate(null, next, (i % 3) + 1, strategies, random);
    if (next) assert.ok(compare(id, next) < 0);
    next = id;
  }
});

test('并发站点在同一间隙插入产生唯一且有序的标识符', () => {
  const random = mulberry32(99);
  const prev = allocate(null, null, 1, new StrategyTable(random), random);
  const next = allocate(prev, null, 1, new StrategyTable(random), random);
  const seen = new Set<string>();
  for (let site = 1; site <= 64; site++) {
    const r = mulberry32(site);
    const id = allocate(prev, next, site, new StrategyTable(r), r);
    assert.ok(compare(prev, id) < 0 && compare(id, next) < 0);
    const key = JSON.stringify(id);
    assert.ok(!seen.has(key), `标识符冲突: ${key}`);
    seen.add(key);
  }
});

test('验收：开头/结尾 1000 次密集交替插入，深度增长平缓', () => {
  const doc = new LseqDocument(1, mulberry32(2024));
  const N = 1000;
  let expected = '';
  for (let i = 0; i < N; i++) {
    const ch = String.fromCharCode(0x4e00 + (i % 64)); // 插入中文字符也无妨
    if (i % 2 === 0) {
      doc.insert(0, ch); // 开头
      expected = ch + expected;
    } else {
      doc.insert(doc.length, ch); // 结尾
      expected = expected + ch;
    }
  }
  assert.equal(doc.text(), expected, '序列内容正确');

  const s = doc.stats();
  assert.equal(s.count, N);
  // 核心验收：深度增长平缓 —— 对数级，远小于线性（N）
  assert.ok(
    s.maxDepth <= 12,
    `最大深度 ${s.maxDepth} 应 <= 12（对数级增长，实际通常 6~9）`,
  );
  assert.ok(s.avgDepth <= 8, `平均深度 ${s.avgDepth.toFixed(2)} 应 <= 8`);
  // 位宽不爆炸：远小于朴素方案的 O(N) 位宽
  assert.ok(s.maxBits <= 512, `最大位宽 ${s.maxBits} 应 <= 512 bits`);
  console.log(
    `    [密集交替插入] N=${N} maxDepth=${s.maxDepth} ` +
      `avgDepth=${s.avgDepth.toFixed(2)} maxBits=${s.maxBits}`,
  );
});

test('深度增长曲线：规模扩大 10 倍，深度仅缓慢增加', () => {
  const depths: number[] = [];
  for (const n of [100, 1000, 10000]) {
    const doc = new LseqDocument(1, mulberry32(5));
    for (let i = 0; i < n; i++) doc.insert(doc.length, 'x'); // 单向连续插入（最坏情形）
    depths.push(doc.stats().maxDepth);
  }
  console.log(`    [深度曲线] n=100/1000/10000 -> maxDepth=${depths.join('/')}`);
  assert.ok(depths[1] - depths[0] <= 4, '100->1000 深度增量应很小');
  assert.ok(depths[2] - depths[1] <= 4, '1000->10000 深度增量应很小');
});

test('验收：多节点随机全排列合并后保持严格因果序', () => {
  const SITES = 5;
  const ROUNDS = 40;
  const seed = 12345;

  // 1) 各节点并发产生操作（本地因果序），汇入操作池
  const sources: LseqDocument[] = [];
  const pool: Op[] = [];
  for (let s = 0; s < SITES; s++) {
    const doc = new LseqDocument(s + 1, mulberry32(seed + s));
    sources.push(doc);
  }
  // 多轮：每轮每个节点在随机位置插入，并随机删除，随后把操作同步给其他人
  const random = mulberry32(seed);
  for (let round = 0; round < ROUNDS; round++) {
    for (let s = 0; s < SITES; s++) {
      const doc = sources[s];
      const ops: Op[] = [];
      const pos = Math.floor(random() * (doc.length + 1));
      ops.push(doc.insert(pos, `s${s}r${round};`));
      if (doc.length > 4 && random() < 0.3) {
        ops.push(doc.delete(Math.floor(random() * doc.length)));
      }
      // 因果广播：其他节点按序接收
      for (let t = 0; t < SITES; t++) {
        if (t !== s) for (const op of ops) sources[t].apply(op);
      }
      pool.push(...ops);
    }
  }
  const reference = sources[0].text();
  for (let s = 1; s < SITES; s++) {
    assert.equal(sources[s].text(), reference, '因果广播后各节点一致');
  }

  // 2) 全新节点以随机全排列接收同一操作池，必须收敛到同一因果序
  for (let trial = 0; trial < 8; trial++) {
    const r = mulberry32(seed * 100 + trial);
    const replica = new LseqDocument(100 + trial, r);
    replica.merge(shuffle(pool, r));
    assert.equal(
      replica.text(),
      reference,
      `随机排列合并 trial=${trial} 应收敛到因果序结果`,
    );
  }

  // 3) 逐操作随机顺序送达（而非整批），同样收敛
  const r2 = mulberry32(seed * 7);
  const trickle = new LseqDocument(999, r2);
  for (const op of shuffle(pool, r2)) trickle.apply(op);
  assert.equal(trickle.text(), reference);
  console.log(
    `    [随机排列合并] sites=${SITES} ops=${pool.length} ` +
      `finalLength=${reference.length} 全部收敛`,
  );
});

test('操作幂等：重复送达不改变结果', () => {
  const a = new LseqDocument(1, mulberry32(1));
  const ops: Op[] = [];
  for (let i = 0; i < 50; i++) ops.push(a.insert(Math.floor(i / 2), 'ab'[i % 2]));
  ops.push(a.delete(10));
  const b = new LseqDocument(2, mulberry32(2));
  const r = mulberry32(3);
  b.merge(ops);
  b.merge(shuffle(ops, r)); // 重复 + 乱序
  b.merge(shuffle(ops, r));
  assert.equal(b.text(), a.text());
});

test('delete 先于 insert 到达（墓碑）仍收敛', () => {
  const a = new LseqDocument(1, mulberry32(11));
  const ins = a.insert(0, 'X');
  const del = a.delete(0);
  const b = new LseqDocument(2, mulberry32(12));
  b.apply(del); // 先收到删除
  b.apply(ins); // 后收到插入
  assert.equal(b.text(), '');
  assert.equal(a.text(), '');
});

test('bound 随深度指数翻倍（自适应可变基数）', () => {
  assert.equal(bound(0), 16);
  assert.equal(bound(1), 32);
  assert.equal(bound(2), 64);
  assert.equal(bound(5), 512);
});
