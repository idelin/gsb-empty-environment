import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { deriveSubpath, resolveExports } from "../index.ts";
import type { ExportsField } from "../index.ts";

describe("conditional exports resolver", () => {
  it("按对象键顺序取第一个匹配的活动键", () => {
    const exportsField: ExportsField = {
      ".": {
        development: "./dev.js",
        default: "./prod.js",
      },
    };

    const withDev = resolveExports(exportsField, ".", ["development", "default"]);
    assert.equal(withDev.target, "./dev.js");

    const withoutDev = resolveExports(exportsField, ".", ["default"]);
    assert.equal(withoutDev.target, "./prod.js");
  });

  it("嵌套条件无匹配时回到上层下一个键", () => {
    const exportsField: ExportsField = {
      ".": {
        node: { import: null, require: "./node-require.cjs" },
        default: "./fallback.js",
      },
    };

    // node.import 命中 null => 直接封锁，不回溯
    const blocked = resolveExports(exportsField, ".", [
      "import",
      "node",
      "default",
    ]);
    assert.equal(blocked.blocked, true);
    assert.equal(blocked.target, null);

    // node 对象内没有 require（条件未启用）=> 回溯到 default
    const fallback = resolveExports(exportsField, ".", ["node", "default"]);
    assert.equal(fallback.target, "./fallback.js");
    assert.ok(
      fallback.trace.attempts.some(
        (attempt) =>
          attempt.outcome === "nested-no-match" &&
          attempt.keyPath.join(".") === "..node",
      ),
    );
  });

  it("null 表示封锁且不会落到 default", () => {
    const exportsField: ExportsField = {
      ".": {
        deno: null,
        default: "./prod.js",
      },
    };

    const result = resolveExports(exportsField, ".", ["deno", "default"]);
    assert.equal(result.blocked, true);
    assert.equal(result.found, false);
    assert.deepEqual(result.trace.blockedKeyPath, [".", "deno"]);
    assert.equal(result.trace.winningKeyPath, null);
  });

  it("不活动的键被跳过", () => {
    const exportsField: ExportsField = {
      ".": {
        development: "./dev.js",
        deno: "./deno.js",
        default: "./prod.js",
      },
    };

    const result = resolveExports(exportsField, ".", ["default"]);
    assert.equal(result.target, "./prod.js");
  });

  it("子路径精确匹配与通配替换", () => {
    const exportsField: ExportsField = {
      "./features/x": "./dist/x.js",
      "./features/*": "./dist/features/*.js",
      "./pkg/*": "./vendor/pkg/*/index.js",
    };

    const exact = resolveExports(exportsField, "./features/x", ["default"]);
    assert.equal(exact.target, "./dist/x.js");

    const wildcard = resolveExports(
      exportsField,
      "./features/y",
      ["default"],
    );
    assert.equal(wildcard.target, "./dist/features/y.js");

    const nested = resolveExports(exportsField, "./pkg/z", ["default"]);
    assert.equal(nested.target, "./vendor/pkg/z/index.js");
  });

  it("数组回退：第一项无匹配或封锁时尝试后续项", () => {
    const exportsField: ExportsField = {
      ".": [
        { custom: "./custom.js" },
        { default: "./fallback.js" },
      ],
    };

    const result = resolveExports(exportsField, ".", ["default"]);
    assert.equal(result.target, "./fallback.js");
  });

  it("deriveSubpath 处理普通包与作用域包", () => {
    assert.equal(deriveSubpath("pkg", "pkg"), ".");
    assert.equal(deriveSubpath("pkg/sub/a.js", "pkg"), "./sub/a.js");
    assert.equal(deriveSubpath("@scope/pkg/x", "@scope/pkg"), "./x");
    assert.throws(() => deriveSubpath("other/x", "pkg"));
  });

  it("拒绝逃逸包目录的目标", () => {
    const exportsField: ExportsField = {
      ".": { default: "../../etc/passwd" },
    };
    assert.throws(() => resolveExports(exportsField, ".", ["default"]));
  });
});
