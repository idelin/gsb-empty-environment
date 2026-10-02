import { test } from "node:test";
import assert from "node:assert/strict";
import { AWLWWMap } from "../src/awlwwmap.ts";


/** 规范化快照：排序点集与键，消除插入顺序差异，便于等价性断言 */
function canon(snap: ReturnType<AWLWWMap["snapshot"]>): string {
  const keys: Record<string, unknown> = {};
  for (const k of Object.keys(snap.keys).sort()) {
    const e = snap.keys[k];
    const fields: Record<string, unknown> = {};
    for (const f of Object.keys(e.fields).sort()) fields[f] = e.fields[f];
    keys[k] = { dots: [...e.dots].sort(), fields };
  }
  return JSON.stringify(keys);
}

/** 构造两个已同步的副本（模拟 A、B 通过网络达成一致后的分叉起点） */
function syncedPair(seed?: (m: AWLWWMap) => void): [AWLWWMap, AWLWWMap] {
  const a = new AWLWWMap("A");
  seed?.(a);
  const b = a.clone("B");
  return [a, b];
}

test("验收边界：并发『删除根键』vs『更新键内字段』→ 更新优先，键与字段保留", () => {
  const [a, b] = syncedPair((m) => {
    m.set("user_profile", "name", "alice", 1000);
    m.set("user_profile", "age", 20, 1001);
  });

  // 节点 A：删除根键 user_profile
  a.removeKey("user_profile");
  assert.equal(a.has("user_profile"), false);

  // 节点 B：并发修改 user_profile.age = 25（未观测到 A 的删除）
  b.set("user_profile", "age", 25, 1002);

  // 因果合并（双向都应收敛到同一结果）
  a.merge(b);
  b.merge(a.clone());

  for (const node of [a, b]) {
    assert.equal(node.has("user_profile"), true, "键 user_profile 应被保留");
    assert.equal(node.getField("user_profile", "age"), 25, "并发更新的 age=25 应存活");
    assert.deepEqual(node.get("user_profile"), { age: 25 });
    // 被 A 实际观测并删除的旧字段不应复活
    assert.equal(node.getField("user_profile", "name"), undefined, "已删除的旧字段 name 不应复活");
  }
});

test("非并发删除：删除先于更新被观测 → 键保持删除", () => {
  const [a, b] = syncedPair((m) => m.set("user_profile", "age", 20, 1000));

  a.removeKey("user_profile");
  // B 先同步到 A 的删除，再写入 → 写入基于删除后的状态，键重新出现是新的添加
  b.merge(a.clone());
  assert.equal(b.has("user_profile"), false);

  // 无并发更新时，删除结果稳定收敛
  const c = b.clone("C");
  c.merge(a.clone());
  assert.equal(c.has("user_profile"), false);
});

test("LWW：同一字段并发赋值，时间戳大者胜；时间戳相同按 nodeId 决胜", () => {
  const [a, b] = syncedPair();
  a.set("k", "f", "from-A", 1000);
  b.set("k", "f", "from-B", 1001);

  a.merge(b);
  b.merge(a.clone());
  assert.equal(a.getField("k", "f"), "from-B");
  assert.equal(b.getField("k", "f"), "from-B");

  // 相同时间戳：nodeId 字典序大者胜（确定性仲裁）
  const [c, d] = syncedPair();
  c.set("k", "f", "from-A", 2000);
  d.set("k", "f", "from-D", 2000);
  c.merge(d);
  d.merge(c.clone());
  assert.equal(c.getField("k", "f"), "from-D");
  assert.equal(d.getField("k", "f"), "from-D");
});

test("子字段动态增删改：字段级删除与并发字段更新按 LWW 仲裁", () => {
  const [a, b] = syncedPair((m) => m.set("profile", "city", "BJ", 1000));

  a.removeField("profile", "city", 1002); // A 删字段（较新）
  b.set("profile", "city", "SH", 1001);   // B 并发改字段（较旧）

  a.merge(b);
  b.merge(a.clone());
  assert.equal(a.getField("profile", "city"), undefined, "较新的删除应胜出");
  assert.equal(b.getField("profile", "city"), undefined);
  assert.equal(a.has("profile"), true, "键本身仍存活");

  // 反向：更新较新 → 更新胜出
  const [c, d] = syncedPair((m) => m.set("profile", "city", "BJ", 1000));
  c.removeField("profile", "city", 1001);
  d.set("profile", "city", "SZ", 1002);
  c.merge(d);
  assert.equal(c.getField("profile", "city"), "SZ");
});

test("键级删除不波及并发新增的其他字段（Add-Wins 对每个并发更新独立生效）", () => {
  const [a, b] = syncedPair((m) => m.set("doc", "title", "t0", 1000));

  a.removeKey("doc");
  b.set("doc", "title", "t1", 1001); // 并发改已有字段
  b.set("doc", "body", "hello", 1002); // 并发加新字段

  a.merge(b);
  assert.equal(a.has("doc"), true);
  assert.deepEqual(a.get("doc"), { title: "t1", body: "hello" });
});

test("合并性质：幂等、交换、结合", () => {
  const build = (): [AWLWWMap, AWLWWMap, AWLWWMap] => {
    const [a, b] = syncedPair((m) => m.set("k1", "f1", "v1", 1000));
    a.removeKey("k1");
    a.set("k2", "x", 1, 1001);
    b.set("k1", "f2", "v2", 1002);
    const c = b.clone("C");
    c.set("k2", "y", 2, 1003);
    return [a, b, c];
  };

  // 幂等
  {
    const [a, b] = build();
    a.merge(b);
    const once = canon(a.snapshot());
    a.merge(b.clone());
    assert.equal(canon(a.snapshot()), once);
  }
  // 交换律
  {
    const [a, b] = build();
    const ab = a.clone("AB").merge(b.clone());
    const ba = b.clone("BA").merge(a.clone());
    assert.equal(canon(ab.snapshot()), canon(ba.snapshot()));
  }
  // 结合律
  {
    const [a, b, c] = build();
    const left = a.clone("L1").merge(b.clone()).merge(c.clone());
    const right = a.clone("R1").merge(b.clone().merge(c.clone()));
    assert.equal(canon(left.snapshot()), canon(right.snapshot()));
  }
});

test("并发双方各自新增不同键：并集保留", () => {
  const [a, b] = syncedPair();
  a.set("only-a", "f", 1, 1000);
  b.set("only-b", "f", 2, 1000);
  a.merge(b);
  assert.deepEqual(a.keysLive().sort(), ["only-a", "only-b"]);
});

test("并发双方都删除同一键：键保持删除", () => {
  const [a, b] = syncedPair((m) => m.set("k", "f", "v", 1000));
  a.removeKey("k");
  b.removeKey("k");
  a.merge(b);
  assert.equal(a.has("k"), false);
});

test("快照序列化/反序列化后可正常合并（模拟网络传输）", () => {
  const [a, b] = syncedPair((m) => m.set("user_profile", "age", 20, 1000));
  a.removeKey("user_profile");
  b.set("user_profile", "age", 25, 1001);

  // 通过 JSON 快照传输状态
  const wireA = JSON.parse(JSON.stringify(a.snapshot()));
  const wireB = JSON.parse(JSON.stringify(b.snapshot()));
  const merged = AWLWWMap.fromSnapshot(wireA).merge(AWLWWMap.fromSnapshot(wireB));

  assert.equal(merged.has("user_profile"), true);
  assert.equal(merged.getField("user_profile", "age"), 25);
});
