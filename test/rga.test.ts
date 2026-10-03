import { test } from "node:test";
import assert from "node:assert/strict";
import { RGA, compareIds, type Op } from "../src/rga.ts";

/** 构建内容为 'AB' 的初始文档（由站点 init 顺序写入），并克隆到两个副本 */
function makePair(): { a: RGA; b: RGA; initOps: Op[] } {
  const init = new RGA("init");
  const initOps: Op[] = [init.insert(0, "A"), init.insert(1, "B")];
  assert.equal(init.toString(), "AB");
  const a = new RGA("u1");
  const b = new RGA("u2");
  for (const op of initOps) {
    a.apply(op);
    b.apply(op);
  }
  assert.equal(a.toString(), "AB");
  assert.equal(b.toString(), "AB");
  return { a, b, initOps };
}

test("验收场景：两人在 A 后并发插入 X / Y，合并后两端一致且无交错", () => {
  const { a, b } = makePair();

  // 用户1 在 A 后插入 'X'，用户2 并发在 A 后插入 'Y'
  const opX = a.insert(1, "X");
  const opY = b.insert(1, "Y");
  assert.equal(a.toString(), "AXB");
  assert.equal(b.toString(), "AYB");

  // 双向合并
  b.apply(opX);
  a.apply(opY);

  // 两端必须收敛到同一结果
  assert.equal(a.toString(), b.toString());
  // 结果必须是 AXYB 或 AYXB，绝不交错（如 AXBY / ABXY）
  assert.match(a.toString(), /^(AXYB|AYXB)$/);
  // 本实现按 id 降序：u2 > u1，故 Y 在前
  assert.equal(a.toString(), "AYXB");
});

test("操作投递顺序颠倒仍收敛", () => {
  const { a, b, initOps } = makePair();
  const opX = a.insert(1, "X");
  const opY = b.insert(1, "Y");

  const r1 = new RGA("r1");
  const r2 = new RGA("r2");
  // r1 先收 X 后收 Y，r2 先收 Y 后收 X
  for (const op of [...initOps, opX, opY]) r1.apply(op);
  for (const op of [...initOps, opY, opX]) r2.apply(op);

  assert.equal(r1.toString(), r2.toString());
  assert.match(r1.toString(), /^(AXYB|AYXB)$/);
});

test("并发插入块不交错：各自插入多字符片段", () => {
  const { a, b, initOps } = makePair();
  // u1 在 A 后连续插入 "12"，u2 并发在 A 后连续插入 "ab"
  const opsA = [a.insert(1, "1"), a.insert(2, "2")];
  const opsB = [b.insert(1, "a"), b.insert(2, "b")];

  const merged = new RGA("m");
  for (const op of initOps) merged.apply(op);
  // 故意交叉投递
  for (const op of [opsA[0], opsB[0], opsA[1], opsB[1]]) merged.apply(op);

  const s = merged.toString();
  // 两个片段各自保持内部顺序，整体不交错
  assert.match(s, /^A(12ab|ab12)B$/);
});

test("重复投递幂等", () => {
  const { a, b } = makePair();
  const opX = a.insert(1, "X");
  b.apply(opX);
  b.apply(opX);
  b.apply(opX);
  assert.equal(b.toString(), "AXB");
});

test("因果乱序投递：子操作先于父操作到达", () => {
  const init = new RGA("init");
  const op1 = init.insert(0, "A");
  const op2 = init.insert(1, "B");
  const op3 = init.insert(2, "C");

  const r = new RGA("r");
  r.apply(op3); // C 的父节点 B 尚未到达，应挂起
  assert.equal(r.toString(), "");
  r.apply(op1);
  assert.equal(r.toString(), "A");
  r.apply(op2); // B 到达后，C 应自动补挂
  assert.equal(r.toString(), "ABC");
});

test("删除：墓碑机制与并发插入", () => {
  const { a, b } = makePair();
  // u1 删除 B，u2 并发在 B 后插入 Z
  const del = a.delete(1);
  const opZ = b.insert(2, "Z");

  a.apply(opZ);
  b.apply(del);

  assert.equal(a.toString(), "AZ");
  assert.equal(b.toString(), "AZ");
});

test("随机并发操作收敛性（多副本、乱序投递）", () => {
  const sites = ["s1", "s2", "s3"];
  const replicas = sites.map((s) => new RGA(s));
  const allOps: Op[] = [];

  // 三轮：每轮各副本在本地随机位置插入/删除，收集操作
  const rng = (() => {
    let seed = 42;
    return () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  })();

  for (let round = 0; round < 5; round++) {
    for (const r of replicas) {
      const len = r.length;
      if (len > 0 && rng() < 0.3) {
        allOps.push(r.delete(Math.floor(rng() * len)));
      } else {
        const ch = String.fromCharCode(97 + Math.floor(rng() * 26));
        allOps.push(r.insert(Math.floor(rng() * (len + 1)), ch));
      }
    }
  }

  // 以不同顺序把全部操作广播给三个新副本
  const orders = [
    [...allOps],
    [...allOps].reverse(),
    [...allOps].sort(() => rng() - 0.5),
  ];
  const results = orders.map((ops) => {
    const r = new RGA("check");
    for (const op of ops) r.apply(op);
    return r.toString();
  });

  assert.equal(results[0], results[1]);
  assert.equal(results[1], results[2]);
  // 且与各源副本最终状态一致（源副本之间也应已收敛）
  // 注意：源副本未互相同步，此处仅验证同一操作流任意顺序重放结果一致
});

test("compareIds 降序语义", () => {
  assert.ok(compareIds({ counter: 2, site: "a" }, { counter: 1, site: "z" }) > 0);
  assert.ok(compareIds({ counter: 1, site: "b" }, { counter: 1, site: "a" }) > 0);
  assert.equal(compareIds({ counter: 1, site: "a" }, { counter: 1, site: "a" }), 0);
});
