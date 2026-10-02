import { test } from "node:test";
import assert from "node:assert/strict";
import { MemberTable, INTERVAL_COUNT } from "../src/membership.ts";

test("合并规则：incarnation 大者胜出（LWW）", () => {
  const t = new MemberTable();
  t.upsert({ id: "n1", state: "ALIVE", incarnation: 5 });
  assert.equal(t.upsert({ id: "n1", state: "DEAD", incarnation: 3 }), false); // 旧版本被拒绝
  assert.equal(t.get("n1")!.state, "ALIVE");
  assert.equal(t.upsert({ id: "n1", state: "DEAD", incarnation: 6 }), true);
  assert.equal(t.get("n1")!.state, "DEAD");
});

test("合并规则：同版本状态优先级 DEAD > SUSPECT > ALIVE", () => {
  const t = new MemberTable();
  t.upsert({ id: "n1", state: "SUSPECT", incarnation: 1 });
  assert.equal(t.upsert({ id: "n1", state: "ALIVE", incarnation: 1 }), false);
  assert.equal(t.upsert({ id: "n1", state: "DEAD", incarnation: 1 }), true);
  assert.equal(t.get("n1")!.state, "DEAD");
});

test("成员表 Digest 与全量重算一致（增量维护正确性）", () => {
  const t = new MemberTable();
  t.upsert({ id: "a", state: "ALIVE", incarnation: 1 });
  t.upsert({ id: "b", state: "ALIVE", incarnation: 1 });
  t.upsert({ id: "a", state: "DEAD", incarnation: 2 });
  const rebuilt = new MemberTable();
  rebuilt.upsert({ id: "a", state: "DEAD", incarnation: 2 });
  rebuilt.upsert({ id: "b", state: "ALIVE", incarnation: 1 });
  assert.equal(t.digest, rebuilt.digest);
  assert.deepEqual(t.intervalDigests(), rebuilt.intervalDigests());
});

test("区间摘要可定位分歧区间", () => {
  const x = new MemberTable();
  const y = new MemberTable();
  for (const id of ["m1", "m2", "m3", "m4", "m5", "m6", "m7"]) {
    const r = { id, state: "ALIVE" as const, incarnation: 1 };
    x.upsert(r);
    y.upsert(r);
  }
  y.upsert({ id: "m8", state: "ALIVE", incarnation: 1 }); // 仅 y 多一个成员
  assert.notEqual(x.digest, y.digest);
  const xi = x.intervalDigests();
  const yi = y.intervalDigests();
  const diff = Array.from({ length: INTERVAL_COUNT }, (_, i) => i).filter((i) => xi[i] !== yi[i]);
  assert.equal(diff.length, 1); // 分歧被精确定位到单个区间
  assert.ok(y.recordsInInterval(diff[0]).some((r) => r.id === "m8"));
});
