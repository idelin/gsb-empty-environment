import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { ChecksumIndex } from "../src/checksum.ts";
import { hashString, isZeroHash } from "../src/hash.ts";
import { hashRow, TableStore, type Row } from "../src/store.ts";

describe("hashString", () => {
  it("相同输入哈希确定一致", () => {
    assert.deepEqual(hashString("hello"), hashString("hello"));
  });
  it("不同输入哈希不同", () => {
    assert.notDeepEqual(hashString("hello"), hashString("hellp"));
  });
});

describe("ChecksumIndex 滚动 XOR", () => {
  it("插入后删除，校验和归零（XOR 自逆）", () => {
    const idx = new ChecksumIndex(8);
    const h = hashString("row-1");
    idx.apply("k1", null, h);
    assert.ok(!isZeroHash(idx.leaf(idx.partitionOf("k1"))));
    idx.apply("k1", h, null);
    assert.ok(isZeroHash(idx.leaf(idx.partitionOf("k1"))));
    assert.equal(idx.totalRows(), 0);
  });

  it("增量维护与全量重算结果一致", () => {
    const rows: Row[] = Array.from({ length: 5000 }, (_, i) => ({
      id: "k" + i,
      v: i * 3,
      s: "x".repeat(i % 17),
    }));
    const incremental = new ChecksumIndex(64);
    for (const r of rows) incremental.apply(r.id, null, hashRow(r));
    // 模拟更新：旧哈希 XOR 出、新哈希 XOR 入
    for (let i = 0; i < 500; i++) {
      const oldRow = rows[i];
      const newRow = { ...oldRow, v: oldRow.v as number + 1 };
      incremental.apply(oldRow.id, hashRow(oldRow), hashRow(newRow));
      rows[i] = newRow;
    }
    const rebuilt = new ChecksumIndex(64);
    for (const r of rows) rebuilt.apply(r.id, null, hashRow(r));
    assert.equal(incremental.rootHash(), rebuilt.rootHash());
    assert.equal(incremental.totalRows(), rows.length);
  });

  it("校验和与变更应用顺序无关", () => {
    const a = new ChecksumIndex(32);
    const b = new ChecksumIndex(32);
    const ops: Array<[string, number]> = Array.from({ length: 2000 }, (_, i) => [
      "pk" + (i % 500),
      i,
    ]);
    for (const [pk, v] of ops) a.apply(pk, null, hashString(pk + ":" + v));
    for (const [pk, v] of [...ops].reverse()) {
      b.apply(pk, null, hashString(pk + ":" + v));
    }
    assert.equal(a.rootHash(), b.rootHash());
  });

  it("Merkle 根对单分区变化敏感", () => {
    const a = new ChecksumIndex(16);
    const b = new ChecksumIndex(16);
    assert.equal(a.rootHash(), b.rootHash());
    b.apply("intruder", null, hashString("evil"));
    assert.notEqual(a.rootHash(), b.rootHash());
    assert.deepEqual(a.mismatchingPartitions(b), [a.partitionOf("intruder")]);
  });

  it("partitionCount 必须为 2 的幂", () => {
    assert.throws(() => new ChecksumIndex(100));
  });
});

describe("TableStore", () => {
  it("put/delete 驱动校验和与行数同步变化", () => {
    const store = new TableStore("t", 64);
    store.put({ id: "a", v: 1 });
    store.put({ id: "b", v: 2 });
    assert.equal(store.size, 2);
    assert.equal(store.index.totalRows(), 2);
    store.put({ id: "a", v: 9 }); // 覆盖不增加行数
    assert.equal(store.index.totalRows(), 2);
    store.delete("a");
    assert.equal(store.index.totalRows(), 1);
    assert.equal(store.get("a"), undefined);
  });

  it("rowsInPartition 只返回该分区的行", () => {
    const store = new TableStore("t", 16);
    for (let i = 0; i < 500; i++) store.put({ id: "r" + i, v: i });
    for (let p = 0; p < 16; p++) {
      for (const row of store.rowsInPartition(p)) {
        assert.equal(store.index.partitionOf(row.id), p);
      }
    }
  });
});
