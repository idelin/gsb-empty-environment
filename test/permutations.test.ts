import test from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";
import type { Event } from "../src/cluster.ts";
import { VectorPrefixModel, orderViolations } from "../src/model.ts";
import { idKey } from "../src/types.ts";
import type { Message, PrefixTable } from "../src/types.ts";

function* permutations<T>(items: readonly T[]): Generator<T[]> {
  if (items.length <= 1) {
    yield [...items];
    return;
  }
  for (let i = 0; i < items.length; i++) {
    const rest = [...items.slice(0, i), ...items.slice(i + 1)];
    for (const tail of permutations(rest)) yield [items[i], ...tail];
  }
}

const msg = (
  node: string,
  epoch: number,
  counter: number,
  deps: PrefixTable,
  payload: string,
): Message => ({ id: { node, epoch, counter }, deps, payload });

test("排列: 因果链乱序+重复到达, 每步与独立模型一致", () => {
  const a1 = msg("A", 1, 1, {}, "a1");
  const a2 = msg("A", 1, 2, { "A@1": 1 }, "a2");
  const b1 = msg("B", 1, 1, { "A@1": 2 }, "b1");
  const arrivals = [a1, a2, b1, a1]; // 含重复副本

  let checked = 0;
  for (const perm of permutations(arrivals)) {
    const c = new Cluster(["A", "B", "R"], 8);
    const model = new VectorPrefixModel();
    for (const m of perm) {
      const res = c.apply({ kind: "recv", at: "R", msg: m });
      assert.notEqual(res?.status, "conflict");
      assert.notEqual(res?.status, "backpressure");
      model.closure([m]);
      // 每一步主实现的交付集合都必须等于模型的不动点
      assert.deepEqual(
        new Set(c.node("R").delivered().map((x) => idKey(x.id))),
        model.deliveredSet(),
      );
    }
    assert.deepEqual(orderViolations(c.node("R").delivered()), []);
    assert.equal(c.node("R").delivered().length, 3); // 重复被幂等吸收
    checked += 1;
  }
  assert.equal(checked, 24);
});

test("排列: epoch 切换 + 确认屏障 + checkpoint 回收 (720 种)", () => {
  const a1 = msg("A", 1, 1, {}, "a1");
  const a2 = msg("A", 1, 2, { "A@1": 1 }, "a2");
  const a3 = msg("A", 2, 1, { "A@1": 2 }, "a3");
  const bogus = msg("A", 1, 3, { "A@1": 2 }, "bogus");

  type Gen = (c: Cluster) => Event;
  const gens: Gen[] = [
    () => ({ kind: "recv", at: "B", msg: a1 }),
    () => ({ kind: "recv", at: "B", msg: a2 }),
    () => ({ kind: "recv", at: "B", msg: a3 }),
    () => ({ kind: "epochSwitch", node: "A", oldEpoch: 1, newEpoch: 2, finalCounter: 2 }),
    () => ({ kind: "ack", from: "A", prefix: { "A@1": 2, "A@2": 1 } }),
    (c) => ({ kind: "ack", from: "B", prefix: c.node("B").snapshotPrefix() }),
  ];

  let checked = 0;
  for (const perm of permutations(gens)) {
    const c = new Cluster(["A", "B"], 8);
    const model = new VectorPrefixModel();
    for (const gen of perm) {
      const ev = gen(c);
      const res = c.apply(ev);
      if (ev.kind === "recv") {
        assert.notEqual(res?.status, "conflict");
        model.closure([ev.msg]);
      }
      assert.ok(c.resources().staged <= 8, "暂存不得超过容量");
    }

    // 不变量: 依赖在前、无重复、无缺口误推进; 与独立模型同集合
    const logB = c.node("B").delivered();
    assert.deepEqual(orderViolations(logB), []);
    assert.deepEqual(
      new Set(logB.map((x) => idKey(x.id))),
      model.deliveredSet(),
    );

    // 退休屏障: 当且仅当 B 实际确认的前缀覆盖了 A@1:2 (A 的确认固定覆盖)
    const expectComplete = (c.confirmedPrefix("B")["A@1"] ?? 0) >= 2;
    assert.equal(c.retirementStatus().length, 1);
    assert.equal(c.retirementStatus()[0].complete, expectComplete);

    c.apply({ kind: "checkpoint" });
    const stable = c.stablePrefix();
    // 回收区间绝不越过共同稳定点
    for (const r of c.reclaimed()) {
      if (r.kind === "dedup") assert.ok(r.to <= (stable[r.prefix] ?? 0));
    }
    if (expectComplete) {
      assert.ok(
        c.reclaimed().some(
          (r) => r.kind === "tombstone" && r.prefix === "A@1" && r.to === 2,
        ),
        "屏障完成则墓碑必须被回收",
      );
      assert.equal(c.resources().tombstones, 0);
    } else {
      assert.ok(!c.reclaimed().some((r) => r.kind === "tombstone"));
      assert.equal(c.resources().tombstones, 1);
    }

    // 恢复后行为一致: 重复幂等(即使 dedup 已回收)、越界旧纪元被拒
    assert.equal(c.apply({ kind: "recv", at: "B", msg: a1 })?.status, "duplicate");
    assert.equal(c.apply({ kind: "recv", at: "B", msg: bogus })?.status, "sealed");
    checked += 1;
  }
  assert.equal(checked, 720);
});

test("排列: 容量为 1 的暂存背压与补洞恢复", () => {
  const c1 = msg("C", 1, 1, {}, "c1");
  const c2 = msg("C", 1, 2, { "C@1": 1 }, "c2");
  const c3 = msg("C", 1, 3, { "C@1": 2 }, "c3");
  const arrivals = [c1, c2, c3, c2]; // 含重复副本

  let checked = 0;
  for (const perm of permutations(arrivals)) {
    const c = new Cluster(["C", "R"], 1);
    const rejected: Message[] = [];
    for (const m of perm) {
      const stagedBefore = c.resources().staged;
      const res = c.apply({ kind: "recv", at: "R", msg: m });
      assert.notEqual(res?.status, "conflict");
      if (res?.status === "backpressure") {
        assert.equal(stagedBefore, 1, "只有暂存满才允许背压");
        rejected.push(m);
      }
      assert.ok(c.resources().staged <= 1);
    }
    // 重试被背压的消息直到不动点
    let progress = true;
    let guard = 0;
    while (progress && guard < 16) {
      progress = false;
      guard += 1;
      for (const m of [...rejected]) {
        const res = c.apply({ kind: "recv", at: "R", msg: m });
        if (res?.status === "delivered" || res?.status === "duplicate") {
          rejected.splice(rejected.indexOf(m), 1);
          progress = true;
        }
      }
    }
    assert.equal(rejected.length, 0, "补洞后所有被背压的消息最终可交付");
    const log = c.node("R").delivered();
    assert.deepEqual(orderViolations(log), []);
    assert.deepEqual(log.map((x) => idKey(x.id)), ["C@1#1", "C@1#2", "C@1#3"]);
    checked += 1;
  }
  assert.equal(checked, 24);
});
