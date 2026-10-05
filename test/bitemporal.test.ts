import assert from "node:assert/strict";
import { test } from "node:test";
import { IndexedStore } from "../src/indexed-store.ts";
import { ScanStore } from "../src/scan-store.ts";
import type { QueryResult } from "../src/types.ts";

type Attrs = { value: string };

function makeStores(): { indexed: IndexedStore<Attrs>; scan: ScanStore<Attrs> } {
  return { indexed: new IndexedStore<Attrs>(), scan: new ScanStore<Attrs>() };
}

function assertSame(
  indexed: QueryResult<Attrs> | null,
  scan: QueryResult<Attrs> | null,
  context: string,
): void {
  assert.deepEqual(indexed, scan, context);
}

test("业务区间为半开区间 [validFrom, validTo)", () => {
  const { indexed, scan } = makeStores();
  indexed.record({ fileId: "f", validFrom: 10, validTo: 20, attributes: { value: "a" } });
  scan.record({ fileId: "f", validFrom: 10, validTo: 20, attributes: { value: "a" } });
  const k = indexed.currentVersion;
  assert.equal(indexed.query("f", 9, k), null);
  assert.equal(indexed.query("f", 10, k)?.attributes.value, "a");
  assert.equal(indexed.query("f", 19, k)?.attributes.value, "a");
  assert.equal(indexed.query("f", 20, k), null);
  for (const t of [9, 10, 19, 20]) {
    assertSame(indexed.query("f", t, k), scan.query("f", t, k), `t=${t}`);
  }
});

test("冲突按录入版本决胜，同版本按修订 ID 决胜", () => {
  const { indexed, scan } = makeStores();
  // 同一业务时点被两条修订覆盖：后录入者（版本更高）胜出
  indexed.record({ fileId: "f", validFrom: 0, validTo: 100, attributes: { value: "old" } });
  indexed.record({ fileId: "f", validFrom: 0, validTo: 100, attributes: { value: "new" } });
  // 显式同录入版本两条：修订 ID 大者胜出
  indexed.record({ fileId: "g", validFrom: 0, validTo: 100, attributes: { value: "r1" }, knowledgeVersion: 10 });
  indexed.record({ fileId: "g", validFrom: 0, validTo: 100, attributes: { value: "r2" }, knowledgeVersion: 10 });
  scan.record({ fileId: "f", validFrom: 0, validTo: 100, attributes: { value: "old" } });
  scan.record({ fileId: "f", validFrom: 0, validTo: 100, attributes: { value: "new" } });
  scan.record({ fileId: "g", validFrom: 0, validTo: 100, attributes: { value: "r1" }, knowledgeVersion: 10 });
  scan.record({ fileId: "g", validFrom: 0, validTo: 100, attributes: { value: "r2" }, knowledgeVersion: 10 });

  assert.equal(indexed.query("f", 50, 1)?.attributes.value, "old");
  assert.equal(indexed.query("f", 50, 2)?.attributes.value, "new");
  const g = indexed.query("g", 50, 10);
  assert.equal(g?.attributes.value, "r2");
  assert.equal(g?.revision.revisionId, 4);
  for (const [file, t, k] of [["f", 50, 1], ["f", 50, 2], ["g", 50, 10]] as const) {
    assertSame(indexed.query(file, t, k), scan.query(file, t, k), `${file}@${t},${k}`);
  }
});

test("迟到补录：旧业务区间在新录入版本才可见，且不影响既有查询", () => {
  const { indexed, scan } = makeStores();
  const ops = (s: IndexedStore<Attrs> | ScanStore<Attrs>) => {
    s.record({ fileId: "f", validFrom: 100, validTo: 200, attributes: { value: "current" } }); // kv=1
    s.record({ fileId: "f", validFrom: 0, validTo: 50, attributes: { value: "backfill" } }); // kv=2 迟到补录
  };
  ops(indexed);
  ops(scan);

  // 补录前（k=1）该业务时点无数据；补录后（k=2）可见
  assert.equal(indexed.query("f", 25, 1), null);
  assert.equal(indexed.query("f", 25, 2)?.attributes.value, "backfill");
  // 补录不影响已有区间的查询结果
  assert.equal(indexed.query("f", 150, 2)?.attributes.value, "current");
  for (const [t, k] of [[25, 1], [25, 2], [150, 1], [150, 2]] as const) {
    assertSame(indexed.query("f", t, k), scan.query("f", t, k), `t=${t},k=${k}`);
  }
});

test("撤销最新修订：只影响之后的知悉版本，查询回退到先前修订", () => {
  const { indexed, scan } = makeStores();
  const ops = (s: IndexedStore<Attrs> | ScanStore<Attrs>) => {
    s.record({ fileId: "f", validFrom: 0, attributes: { value: "v1" } }); // kv=1
    const latest = s.record({ fileId: "f", validFrom: 0, attributes: { value: "v2" } }); // kv=2
    s.revoke(latest.revisionId); // kv=3
  };
  ops(indexed);
  ops(scan);

  // 撤销前的知悉版本仍可见 v2（撤销只影响之后的知悉版本）
  assert.equal(indexed.query("f", 10, 2)?.attributes.value, "v2");
  // 撤销版本起回退到 v1
  assert.equal(indexed.query("f", 10, 3)?.attributes.value, "v1");
  assert.equal(indexed.query("f", 10, 3)?.revision.revisionId, 1);
  for (const k of [1, 2, 3, 100]) {
    assertSame(indexed.query("f", 10, k), scan.query("f", 10, k), `k=${k}`);
  }
});

test("同一文件跨多个业务区间", () => {
  const { indexed, scan } = makeStores();
  const ops = (s: IndexedStore<Attrs> | ScanStore<Attrs>) => {
    s.record({ fileId: "f", validFrom: 0, validTo: 10, attributes: { value: "a" } });
    s.record({ fileId: "f", validFrom: 10, validTo: 20, attributes: { value: "b" } });
    s.record({ fileId: "f", validFrom: 30, validTo: 40, attributes: { value: "c" } });
    s.record({ fileId: "f", validFrom: 50, attributes: { value: "open" } }); // 开放区间
  };
  ops(indexed);
  ops(scan);

  const cases: Array<[number, string | null]> = [
    [0, "a"], [9, "a"], [10, "b"], [19, "b"], [20, null], [29, null],
    [30, "c"], [39, "c"], [40, null], [49, null], [50, "open"], [10_000, "open"],
  ];
  const k = indexed.currentVersion;
  for (const [t, expected] of cases) {
    assert.equal(indexed.query("f", t, k)?.attributes.value ?? null, expected, `t=${t}`);
    assertSame(indexed.query("f", t, k), scan.query("f", t, k), `t=${t}`);
  }
});

test("非法输入与重复撤销报错", () => {
  const store = new IndexedStore<Attrs>();
  assert.throws(() => store.record({ fileId: "f", validFrom: 10, validTo: 10, attributes: { value: "x" } }));
  assert.throws(() => store.record({ fileId: "f", validFrom: 20, validTo: 10, attributes: { value: "x" } }));
  const rev = store.record({ fileId: "f", validFrom: 0, attributes: { value: "x" } });
  assert.throws(() => store.record({ fileId: "f", validFrom: 0, attributes: { value: "y" }, knowledgeVersion: 0 }));
  store.revoke(rev.revisionId);
  assert.throws(() => store.revoke(rev.revisionId));
  assert.throws(() => store.revoke(9999));
});

test("随机操作下索引实现与逐条扫描参考一致", () => {
  // 可复现的伪随机数
  let state = 0x2f6e2b1b;
  const rand = () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const pick = (n: number) => Math.floor(rand() * n);

  const indexed = new IndexedStore<Attrs>();
  const scan = new ScanStore<Attrs>();
  const files = ["f1", "f2", "f3"];
  const live: number[] = [];

  for (let step = 0; step < 400; step++) {
    const action = rand();
    if (action < 0.75 || live.length === 0) {
      const fileId = files[pick(files.length)];
      const validFrom = pick(200);
      const validTo = rand() < 0.25 ? undefined : validFrom + 1 + pick(40);
      const input = {
        fileId,
        validFrom,
        validTo,
        attributes: { value: `s${step}` },
        // 偶尔显式重复当前录入版本，制造同版本多修订
        knowledgeVersion: rand() < 0.15 ? indexed.currentVersion : undefined,
      };
      live.push(indexed.record(input).revisionId);
      scan.record(input);
    } else {
      // 随机撤销一条未撤销的修订（含最新修订）
      const target = live.splice(pick(live.length), 1)[0];
      indexed.revoke(target);
      scan.revoke(target);
    }
  }

  // 密集双时点查询交叉核对
  const maxK = indexed.currentVersion;
  for (const fileId of [...files, "missing"]) {
    for (let t = 0; t <= 240; t += 3) {
      for (let k = 0; k <= maxK + 1; k += 1) {
        assertSame(indexed.query(fileId, t, k), scan.query(fileId, t, k), `${fileId}@${t},${k}`);
      }
    }
  }
});
