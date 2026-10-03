import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { LSMTree } from "../src/index.ts";

async function makeTree(options = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "lsm-tombstone-"));
  const tree = await LSMTree.open(dir, {
    memtableFlushThreshold: Number.MAX_SAFE_INTEGER,
    l0CompactionThreshold: Number.MAX_SAFE_INTEGER,
    levelCapacities: [Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER],
    maxLevels: 4,
    ...options,
  });
  const cleanup = async () => fs.rm(dir, { recursive: true, force: true });
  return { tree, dir, cleanup };
}

async function rawDiskContents(dir: string): Promise<string> {
  const files = (await fs.readdir(dir)).filter((f) => f.endsWith(".json")).sort();
  const parts = [];
  for (const file of files) {
    parts.push(await fs.readFile(path.join(dir, file), "utf8"));
  }
  return parts.join("\n");
}

test("删除后立即查询返回 NotFound（MemTable 中的 Tombstone 屏蔽）", async () => {
  const { tree, cleanup } = await makeTree();
  try {
    await tree.put("K1", "V1");
    assert.equal(await tree.get("K1"), "V1");
    await tree.delete("K1");
    assert.equal(await tree.get("K1"), undefined);
  } finally {
    await cleanup();
  }
});

test("Tombstone 落盘后继续屏蔽 SSTable 中的旧值", async () => {
  const { tree, cleanup } = await makeTree();
  try {
    await tree.put("K1", "V1");
    await tree.flush();
    await tree.delete("K1");
    assert.equal(await tree.get("K1"), undefined);
    await tree.flush();
    assert.equal(await tree.get("K1"), undefined);
    const hits = tree.findOnDisk("K1");
    assert.equal(hits.length, 2);
    assert.ok(hits.some((h) => h.entry.value === "V1"));
    assert.ok(hits.some((h) => h.entry.value === null));
  } finally {
    await cleanup();
  }
});

test("Range Scan 跳过 Tombstone 与被覆盖的旧版本", async () => {
  const { tree, cleanup } = await makeTree();
  try {
    await tree.put("a", "1");
    await tree.put("b", "2");
    await tree.put("c", "3");
    await tree.put("d", "4");
    await tree.flush();
    await tree.delete("b");
    await tree.put("c", "33");
    await tree.delete("d");
    assert.deepEqual(await tree.scan(), [
      ["a", "1"],
      ["c", "33"],
    ]);
    await tree.flush();
    await tree.compact();
    assert.deepEqual(await tree.scan(), [
      ["a", "1"],
      ["c", "33"],
    ]);
    assert.deepEqual(await tree.scan("b", "d"), [["c", "33"]]);
  } finally {
    await cleanup();
  }
});

test("删除后重新写入可复活该 Key（seq 决定新旧）", async () => {
  const { tree, cleanup } = await makeTree();
  try {
    await tree.put("K1", "V1");
    await tree.flush();
    await tree.delete("K1");
    await tree.flush();
    await tree.put("K1", "V9");
    assert.equal(await tree.get("K1"), "V9");
    await tree.flush();
    await tree.compact();
    assert.equal(await tree.get("K1"), "V9");
  } finally {
    await cleanup();
  }
});

test("Compaction 丢弃被覆盖的旧版本（非最底层也生效）", async () => {
  const { tree, cleanup } = await makeTree();
  try {
    await tree.put("K1", "V1");
    await tree.flush();
    await tree.put("K1", "V2");
    await tree.flush();
    await tree.compactLevel(0);
    const hits = tree.findOnDisk("K1");
    assert.equal(hits.length, 1);
    assert.equal(hits[0].entry.value, "V2");
    assert.equal(await tree.get("K1"), "V2");
  } finally {
    await cleanup();
  }
});

test("验收流程：多轮 Compaction 后 Tombstone 在最底层被物理擦除", async () => {
  const { tree, dir, cleanup } = await makeTree();
  try {
    await tree.put("K1", "V1");
    await tree.put("F1", "filler");
    await tree.flush();

    await tree.compactLevel(0);
    await tree.compactLevel(1);
    await tree.compactLevel(2);
    let hits = tree.findOnDisk("K1");
    assert.equal(hits.length, 1);
    assert.equal(hits[0].level, 3);
    assert.equal(hits[0].entry.value, "V1");
    assert.equal(await tree.get("K1"), "V1");

    await tree.delete("K1");
    assert.equal(await tree.get("K1"), undefined, "删除后立即 NotFound");

    await tree.flush();
    hits = tree.findOnDisk("K1");
    assert.equal(hits.length, 2, "Tombstone 与旧值并存于磁盘");
    assert.ok(hits.some((h) => h.level === 0 && h.entry.value === null));
    assert.ok(hits.some((h) => h.level === 3 && h.entry.value === "V1"));

    await tree.compactLevel(0);
    hits = tree.findOnDisk("K1");
    assert.equal(hits.length, 2, "L1 非最底层，Tombstone 必须保留");
    assert.ok(hits.some((h) => h.level === 1 && h.entry.value === null));
    assert.ok(hits.some((h) => h.level === 3 && h.entry.value === "V1"));

    await tree.compactLevel(1);
    hits = tree.findOnDisk("K1");
    assert.equal(hits.length, 2, "L2 非最底层，Tombstone 继续下沉");
    assert.ok(hits.some((h) => h.level === 2 && h.entry.value === null));

    await tree.compactLevel(2);
    hits = tree.findOnDisk("K1");
    assert.equal(hits.length, 0, "最底层合并后 Tombstone 与旧值被彻底擦除");
    assert.equal(await tree.get("K1"), undefined);
    assert.deepEqual(await tree.scan("K1", "K1"), []);

    const raw = await rawDiskContents(dir);
    assert.ok(!raw.includes('"K1"'), "磁盘文件中不再出现 K1 的任何痕迹");
    assert.ok(raw.includes('"F1"'), "未删除的 Key 不受影响");
  } finally {
    await cleanup();
  }
});

test("删除不存在的 Key：Tombstone 到达最底层后同样被擦除", async () => {
  const { tree, dir, cleanup } = await makeTree();
  try {
    await tree.delete("ghost");
    await tree.flush();
    await tree.compactLevel(0);
    assert.equal(tree.findOnDisk("ghost").length, 0, "L1 即最底层，Tombstone 直接消除");
    assert.equal(await tree.get("ghost"), undefined);
    const raw = await rawDiskContents(dir);
    assert.ok(!raw.includes("ghost"));
  } finally {
    await cleanup();
  }
});

test("自动 Flush/Compaction 下与模型一致的随机一致性测试", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "lsm-tombstone-rand-"));
  const tree = await LSMTree.open(dir, {
    memtableFlushThreshold: 3,
    l0CompactionThreshold: 2,
    levelCapacities: [4, 8, 16],
    maxLevels: 4,
  });
  try {
    const model = new Map<string, string>();
    let random = 42;
    const next = () => (random = (random * 1103515245 + 12345) % 2147483648);
    const keys = Array.from({ length: 12 }, (_, i) => `key-${i}`);
    for (let op = 0; op < 400; op++) {
      const key = keys[next() % keys.length];
      if (next() % 3 === 0) {
        await tree.delete(key);
        model.delete(key);
      } else {
        const value = `v${op}`;
        await tree.put(key, value);
        model.set(key, value);
      }
    }
    await tree.flush();
    for (const key of keys) {
      assert.equal(await tree.get(key), model.get(key), `get(${key})`);
    }
    const expected = [...model.entries()].sort(([a], [b]) => (a < b ? -1 : 1));
    assert.deepEqual(await tree.scan(), expected);
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});
