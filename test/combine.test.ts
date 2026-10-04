import { test } from "node:test";
import assert from "node:assert/strict";
import { selectCompatibleFixes } from "../src/select.ts";
import type {
  CombineInput,
  FixSuggestion,
  TextEdit,
} from "../src/types.ts";

function edit(
  file: string,
  sl: number,
  sc: number,
  el: number,
  ec: number,
  newText: string,
): TextEdit {
  return {
    file,
    range: { start: { line: sl, character: sc }, end: { line: el, character: ec } },
    newText,
  };
}

function suggestion(partial: Partial<FixSuggestion> & { id: string }): FixSuggestion {
  return {
    description: partial.id,
    benefit: 1,
    edits: [],
    preconditions: [],
    ...partial,
  };
}

function run(input: CombineInput) {
  return selectCompatibleFixes(input);
}

test("重叠编辑冲突：单独都适用，合在一起只能选收益大的", () => {
  const files = { "a.ts": "const x = 1;\n" };
  const result = run({
    files,
    suggestions: [
      suggestion({
        id: "big",
        benefit: 10,
        edits: [edit("a.ts", 0, 10, 0, 11, "2")],
      }),
      suggestion({
        id: "small",
        benefit: 5,
        edits: [edit("a.ts", 0, 10, 0, 11, "3")],
      }),
    ],
  });
  assert.deepEqual(
    result.selected.map((s) => s.id),
    ["big"],
  );
  assert.equal(result.totalBenefit, 10);
  const rejected = result.rejected.find((r) => r.id === "small")!;
  const conflict = rejected.reasons.find((r) => r.kind === "conflict");
  assert.ok(conflict, "应输出冲突证据");
  if (conflict?.kind === "conflict") {
    assert.equal(conflict.evidence[0]!.kind, "edit-overlap");
    assert.equal(conflict.evidence[0]!.otherId, "big");
    assert.match(conflict.evidence[0]!.detail, /a\.ts/);
  }
  assert.equal(result.files["a.ts"], "const x = 2;\n");
});

test("插入点落在他人替换区间内也算冲突（不能只看范围不相交）", () => {
  const files = { "a.ts": "abcdef\n" };
  const result = run({
    files,
    suggestions: [
      suggestion({
        id: "replace",
        benefit: 10,
        edits: [edit("a.ts", 0, 1, 0, 5, "X")],
      }),
      suggestion({
        id: "insert-inside",
        benefit: 5,
        edits: [edit("a.ts", 0, 3, 0, 3, "Y")],
      }),
    ],
  });
  assert.deepEqual(
    result.selected.map((s) => s.id),
    ["replace"],
  );
  assert.ok(result.rejected.some((r) => r.id === "insert-inside"));
});

test("语义冲突标签：文本范围完全不重叠也互斥", () => {
  const files = { "a.ts": "line0\nline1\n" };
  const result = run({
    files,
    suggestions: [
      suggestion({
        id: "plan-a",
        benefit: 7,
        edits: [edit("a.ts", 0, 0, 0, 0, "// A\n")],
        conflictKeys: ["same-decision"],
      }),
      suggestion({
        id: "plan-b",
        benefit: 6,
        edits: [edit("a.ts", 1, 0, 1, 0, "// B\n")],
        conflictKeys: ["same-decision"],
      }),
    ],
  });
  assert.deepEqual(
    result.selected.map((s) => s.id),
    ["plan-a"],
  );
  const rejected = result.rejected.find((r) => r.id === "plan-b")!;
  const conflict = rejected.reasons.find((r) => r.kind === "conflict");
  assert.ok(conflict);
  if (conflict?.kind === "conflict") {
    assert.equal(conflict.evidence[0]!.kind, "conflict-key");
  }
});

test("前置关系：选中依赖项会带上前置；前置被排除则依赖项也被排除", () => {
  const files = { "a.ts": "foo\n", "b.ts": "bar\n" };
  const result = run({
    files,
    suggestions: [
      suggestion({
        id: "base",
        benefit: 1,
        edits: [edit("a.ts", 0, 0, 0, 3, "baz")],
      }),
      suggestion({
        id: "dependent",
        benefit: 10,
        edits: [edit("b.ts", 0, 0, 0, 3, "qux")],
        requires: ["base"],
      }),
      // 与 base 冲突且收益更高 → base 被挤掉，dependent 也不能选
      suggestion({
        id: "base-rival",
        benefit: 2,
        edits: [edit("a.ts", 0, 0, 0, 3, "quux")],
      }),
    ],
  });
  const ids = result.selected.map((s) => s.id).sort();
  // base(1)+dependent(10)=11 > base-rival(2)，应选 base+dependent
  assert.deepEqual(ids, ["base", "dependent"]);
  assert.equal(result.totalBenefit, 11);
  assert.equal(result.files["a.ts"], "baz\n");
  assert.equal(result.files["b.ts"], "qux\n");
});

test("前置被更高收益冲突项挤掉时，依赖项连带排除并给出证据", () => {
  const files = { "a.ts": "foo\n", "b.ts": "bar\n" };
  const result = run({
    files,
    suggestions: [
      suggestion({
        id: "base",
        benefit: 1,
        edits: [edit("a.ts", 0, 0, 0, 3, "baz")],
      }),
      suggestion({
        id: "dependent",
        benefit: 5,
        edits: [edit("b.ts", 0, 0, 0, 3, "qux")],
        requires: ["base"],
      }),
      suggestion({
        id: "base-rival",
        benefit: 100,
        edits: [edit("a.ts", 0, 0, 0, 3, "quux")],
      }),
    ],
  });
  const ids = result.selected.map((s) => s.id);
  assert.deepEqual(ids, ["base-rival"]);
  const rejected = result.rejected.find((r) => r.id === "dependent")!;
  assert.ok(
    rejected.reasons.some(
      (r) => r.kind === "requires-excluded" && r.requiredId === "base",
    ),
    "依赖项应因前置未选中而被排除",
  );
});

test("应用后重新检查前置条件：读写干涉导致前置失效的建议被剔除并归因", () => {
  // a 的编辑把 "x = 1" 改成 "x = 2"；b 的前置条件要求 "x = 1" 仍存在，
  // 且 b 的编辑会删掉 a 的前置条件所依赖的标记 —— 循环干涉，无法共存。
  const files = {
    "config.ts": "export const x = 1;\n",
    "main.ts": "// enable-feature\nmain();\n",
  };
  const result = run({
    files,
    suggestions: [
      suggestion({
        id: "bump-x",
        benefit: 10,
        edits: [edit("config.ts", 0, 17, 0, 18, "2")],
        preconditions: [
          { kind: "contains", file: "main.ts", pattern: "enable-feature" },
        ],
      }),
      suggestion({
        id: "cleanup-flag",
        benefit: 4,
        edits: [edit("main.ts", 0, 0, 1, 0, "")],
        preconditions: [
          { kind: "contains", file: "config.ts", pattern: "x = 1" },
        ],
      }),
    ],
  });
  // 两者文本范围不重叠，但互为读写干涉：无论谁先应用，另一方前置都失效。
  // 求解器先都选，模拟应用后剔除收益小的 cleanup-flag 并重解。
  assert.deepEqual(
    result.selected.map((s) => s.id),
    ["bump-x"],
  );
  const rejected = result.rejected.find((r) => r.id === "cleanup-flag")!;
  const failed = rejected.reasons.find(
    (r) => r.kind === "precondition-failed-after-apply",
  );
  assert.ok(failed, "应给出前置条件失效证据");
  if (failed?.kind === "precondition-failed-after-apply") {
    assert.deepEqual(failed.invalidatedBy, ["bump-x"]);
    assert.match(failed.detail, /x = 1/);
  }
  assert.equal(result.files["config.ts"], "export const x = 2;\n");
});

test("读写干涉但顺序可行时，两者都保留（延迟重试）", () => {
  const files = { "a.ts": "v = 1;\nw = 0;\n" };
  const result = run({
    files,
    suggestions: [
      // 按 id 排序 b 先尝试，但其前置在 a 应用后才成立 → 延迟重试应成功
      suggestion({
        id: "a-first",
        benefit: 3,
        edits: [edit("a.ts", 0, 4, 0, 5, "2")],
      }),
      suggestion({
        id: "b-second",
        benefit: 4,
        edits: [edit("a.ts", 1, 4, 1, 5, "9")],
        preconditions: [{ kind: "contains", file: "a.ts", pattern: "v = 2" }],
      }),
    ],
  });
  assert.equal(result.totalBenefit, 7);
  assert.equal(result.files["a.ts"], "v = 2;\nw = 9;\n");
});

test("最多 25 项：超出时按收益取舍并给出容量证据", () => {
  const files: Record<string, string> = {};
  const suggestions: FixSuggestion[] = [];
  for (let i = 0; i < 30; i++) {
    const file = `f${i}.ts`;
    files[file] = "x\n";
    suggestions.push(
      suggestion({
        id: `fix-${String(i).padStart(2, "0")}`,
        benefit: i + 1, // 收益 1..30
        edits: [edit(file, 0, 0, 0, 1, "y")],
      }),
    );
  }
  const result = run({ files, suggestions });
  assert.equal(result.selected.length, 25);
  // 应选中收益最高的 25 个（6..30）
  const benefits = result.selected.map((s) => s.benefit);
  assert.equal(Math.min(...benefits), 6);
  assert.equal(result.totalBenefit, (6 + 30) * 25 / 2);
  assert.equal(result.rejected.length, 5);
  for (const r of result.rejected) {
    assert.ok(
      r.reasons.some((reason) => reason.kind === "capacity"),
      `${r.id} 应有容量原因`,
    );
  }
});

test("非法输入：编辑指向不存在的文件 → 拒绝并说明", () => {
  const result = run({
    files: { "a.ts": "x\n" },
    suggestions: [
      suggestion({
        id: "bad",
        benefit: 5,
        edits: [edit("missing.ts", 0, 0, 0, 0, "y")],
      }),
    ],
  });
  assert.equal(result.selected.length, 0);
  const rejected = result.rejected.find((r) => r.id === "bad")!;
  assert.ok(rejected.reasons.some((r) => r.kind === "invalid"));
});

test("requires 环：环上节点整体取舍", () => {
  const files = { "a.ts": "1\n", "b.ts": "2\n" };
  const result = run({
    files,
    suggestions: [
      suggestion({
        id: "ring-a",
        benefit: 3,
        edits: [edit("a.ts", 0, 0, 0, 1, "A")],
        requires: ["ring-b"],
      }),
      suggestion({
        id: "ring-b",
        benefit: 4,
        edits: [edit("b.ts", 0, 0, 0, 1, "B")],
        requires: ["ring-a"],
      }),
    ],
  });
  assert.equal(result.totalBenefit, 7);
  assert.equal(result.selected.length, 2);
});
