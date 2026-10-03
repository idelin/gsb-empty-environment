import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rga, compareId, type RgaOp, type InsertOp } from '../src/rga.ts';

/** 从初始文本构建副本，并把构建操作同步给其他副本。 */
function replicate(text: string, ...replicas: Rga[]): void {
  const builder = replicas[0];
  const ops: RgaOp[] = [];
  for (let i = 0; i < text.length; i++) ops.push(builder.localInsert(i, text[i]));
  for (const r of replicas.slice(1)) r.merge(ops);
}

test('标识比较：先 counter 后 replica 字典序', () => {
  assert.ok(compareId([1, 'a'], [2, 'a']) < 0);
  assert.ok(compareId([2, 'a'], [1, 'a']) > 0);
  assert.ok(compareId([1, 'a'], [1, 'b']) < 0);
  assert.equal(compareId([1, 'a'], [1, 'a']), 0);
});

test('基本插入与删除', () => {
  const rga = new Rga('r1');
  rga.localInsert(0, 'A');
  rga.localInsert(1, 'B');
  rga.localInsert(1, 'X'); // A X B
  assert.equal(rga.toString(), 'AXB');
  rga.localDelete(1);
  assert.equal(rga.toString(), 'AB');
  assert.equal(rga.length, 2);
});

test('验收边界：两人协同编辑 AB，A 后并发插入 X/Y，两端稳定收敛', () => {
  const user1 = new Rga('user1');
  const user2 = new Rga('user2');
  replicate('AB', user1, user2);

  // 并发：都在 'A' 之后（可见下标 1）插入
  const opX = user1.localInsert(1, 'X');
  const opY = user2.localInsert(1, 'Y');
  assert.equal(user1.toString(), 'AXB');
  assert.equal(user2.toString(), 'AYB');

  // 合并后两端必须一致，且结果为 AXYB 或 AYXB，无交错乱序
  user1.apply(opY);
  user2.apply(opX);
  assert.equal(user1.toString(), user2.toString());
  assert.ok(
    user1.toString() === 'AXYB' || user1.toString() === 'AYXB',
    `unexpected result: ${user1.toString()}`,
  );
  // 并发子节点按标识降序：user2 > user1，故 Y 在 X 前
  assert.equal(user1.toString(), 'AYXB');
});

test('收敛与操作到达顺序无关（交换投递顺序）', () => {
  // 公共基底 'AB'
  const setup = new Rga('setup');
  const baseOps: RgaOp[] = [setup.localInsert(0, 'A'), setup.localInsert(1, 'B')];
  const fresh = (): Rga => {
    const r = new Rga('observer');
    r.merge(baseOps);
    return r;
  };

  const user1 = new Rga('user1');
  const user2 = new Rga('user2');
  user1.merge(baseOps);
  user2.merge(baseOps);
  const opX = user1.localInsert(1, 'X');
  const opY = user2.localInsert(1, 'Y');

  // 四个副本按不同顺序接收同一批操作
  const r1 = user1; // 本地已有 X，后收 Y
  r1.apply(opY);
  const r2 = user2; // 本地已有 Y，后收 X
  r2.apply(opX);
  const r3 = fresh(); // 先 X 后 Y
  r3.apply(opX);
  r3.apply(opY);
  const r4 = fresh(); // 先 Y 后 X
  r4.apply(opY);
  r4.apply(opX);

  const expected = r1.toString();
  assert.equal(r2.toString(), expected);
  assert.equal(r3.toString(), expected);
  assert.equal(r4.toString(), expected);
});

test('同一位置多路并发插入按标识降序排列', () => {
  const replicas = [new Rga('a'), new Rga('b'), new Rga('c')];
  replicate('AB', ...replicas);

  const ops: InsertOp[] = [
    replicas[0].localInsert(1, 'X'),
    replicas[1].localInsert(1, 'Y'),
    replicas[2].localInsert(1, 'Z'),
  ];
  // 以不同顺序把操作广播给各副本
  replicas[0].apply(ops[1]);
  replicas[0].apply(ops[2]);
  replicas[1].apply(ops[2]);
  replicas[1].apply(ops[0]);
  replicas[2].apply(ops[0]);
  replicas[2].apply(ops[1]);

  // 标识 [1,'c'] > [1,'b'] > [1,'a']，降序 => Z Y X
  for (const r of replicas) assert.equal(r.toString(), 'AZYXB');
});

test('并发删除与插入：墓碑保证因果引用有效', () => {
  const user1 = new Rga('user1');
  const user2 = new Rga('user2');
  replicate('AB', user1, user2);

  const delB = user1.localDelete(1); // user1 删 B
  const opY = user2.localInsert(1, 'Y'); // user2 在 A 后插 Y
  user1.apply(opY);
  user2.apply(delB);
  assert.equal(user1.toString(), 'AY');
  assert.equal(user2.toString(), 'AY');
});

test('操作幂等：重复应用不改变结果', () => {
  const user1 = new Rga('user1');
  const user2 = new Rga('user2');
  replicate('AB', user1, user2);
  const opX = user1.localInsert(1, 'X');
  user2.apply(opX);
  user2.apply(opX);
  user2.apply(opX);
  assert.equal(user2.toString(), 'AXB');
});

test('随机并发编辑收敛性（100 轮模糊测试）', () => {
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  const chars = 'abcdefghijklmnopqrstuvwxyz';

  for (let round = 0; round < 100; round++) {
    const r1 = new Rga('replica-1');
    const r2 = new Rga('replica-2');
    replicate('AB', r1, r2);

    // 各自离线产生一串随机操作
    const ops1: RgaOp[] = [];
    const ops2: RgaOp[] = [];
    for (let i = 0; i < 20; i++) {
      const idx = Math.floor(rand() * (r1.length + 1));
      ops1.push(r1.localInsert(idx, chars[Math.floor(rand() * 26)]));
      const jdx = Math.floor(rand() * (r2.length + 1));
      ops2.push(r2.localInsert(jdx, chars[Math.floor(rand() * 26)]));
    }

    // 交叉合并（各副本内部保持因果序）
    r1.merge(ops2);
    r2.merge(ops1);

    assert.equal(
      r1.toString(),
      r2.toString(),
      `round ${round}: '${r1.toString()}' vs '${r2.toString()}'`,
    );
  }
});
