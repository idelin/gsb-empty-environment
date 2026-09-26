import { test } from "node:test";
import assert from "node:assert/strict";
import { DocumentModel } from "../src/model.ts";
import type { DocNode } from "../src/types.ts";

test("node tree flattens with inherited container levels", () => {
  const tree: DocNode = {
    id: "root",
    level: 0,
    children: [
      { id: "ltr", text: "ab", clusters: ["a", "b"] },
      {
        id: "rtlbox",
        level: 1,
        children: [
          { id: "r1", text: "cd", clusters: ["c", "d"] },
          {
            id: "nested",
            level: 2,
            children: [{ id: "r2", text: "e", clusters: ["e"] }],
          },
        ],
      },
      { id: "tail", text: "f", clusters: ["f"] },
    ],
  };
  const model = new DocumentModel(tree);
  assert.deepEqual(
    model.getClusters().map((c) => `${c.text}:${c.level}@${c.nodeId}`),
    ["a:0@ltr", "b:0@ltr", "c:1@r1", "d:1@r1", "e:2@r2", "f:0@tail"],
  );
});

test("per-cluster levels override container level and must match count", () => {
  const model = new DocumentModel({
    id: "p",
    children: [{ id: "t", text: "abc", clusters: ["a", "b", "c"], levels: [0, 1, 0] }],
  });
  assert.deepEqual([...model.levelsOf("t")!], [0, 1, 0]);
  assert.throws(
    () =>
      new DocumentModel({
        id: "p",
        children: [{ id: "t", text: "ab", clusters: ["a", "b"], levels: [0] }],
      }),
    /levels length 1 does not match cluster count 2/,
  );
});

test("updateLevels is immutable, local, and bumps the version", () => {
  const model = new DocumentModel({
    id: "p",
    children: [{ id: "t", text: "abcde", clusters: ["a", "b", "c", "d", "e"], levels: [0, 0, 0, 0, 0] }],
  });
  const next = model.updateLevels("t", 1, 3, 1);
  assert.equal(next.version, 1);
  assert.deepEqual([...next.levelsOf("t")!], [0, 1, 1, 1, 0]);
  assert.deepEqual([...model.levelsOf("t")!], [0, 0, 0, 0, 0]);
  assert.throws(() => model.updateLevels("t", 4, 2, 1), /out of bounds/);
  assert.throws(() => model.updateLevels("missing", 0, 1, 1), /Unknown text node/);
});
