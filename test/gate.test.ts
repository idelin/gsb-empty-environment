import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { describe, it } from "node:test";
import { evaluateGate, sha256Hex } from "../index.ts";
import type { ExportsField } from "../index.ts";

const sha = (text: string) =>
  createHash("sha256").update(text, "utf8").digest("hex");

const attackExports: ExportsField = {
  ".": {
    // 恶意依赖把未经审查的 development 入口放在 default 之前
    development: "./dev/main.js",
    node: {
      import: "./dist/main.js",
      default: "./dist/main.cjs",
    },
    default: "./dist/main.cjs",
  },
};

const blockedExports: ExportsField = {
  ".": {
    development: null,
    node: { import: "./dist/main.js", default: "./dist/main.cjs" },
    default: "./dist/main.cjs",
  },
};

const manifestAll = [
  "dist/main.js",
  "dist/main.cjs",
  "dev/main.js",
  "package.json",
];
const manifestWithoutDev = [
  "dist/main.js",
  "dist/main.cjs",
  "package.json",
];

const memoryFs = (files: Record<string, string>) => (relPath: string) =>
  new TextEncoder().encode(files[relPath] ?? "");

const base = {
  packageName: "victim-pkg",
  specifier: "victim-pkg",
  exports: attackExports,
  baselineConditions: ["import", "node", "default"],
  manifest: manifestAll,
};

describe("conditional differential gate", () => {
  it("development 前置条件使入口改变且未审查时拒绝，并标出条件与键", () => {
    const result = evaluateGate({
      ...base,
      additionalConditions: ["development"],
      allowedConditions: ["development", "deno"],
    });

    assert.equal(result.decision, "deny");
    assert.equal(result.reason, "deny-unreviewed-entry");
    assert.equal(result.baseline.target, "./dist/main.js");
    assert.equal(result.augmented.target, "./dev/main.js");
    assert.equal(result.divergence.changed, true);
    assert.equal(result.divergence.condition, "development");
    assert.equal(result.divergence.attribution, "single");
    assert.deepEqual(result.divergence.key, [".", "development"]);
    assert.deepEqual(result.divergence.baselineKey, [
      ".",
      "node",
      "import",
    ]);
    assert.equal(result.manifest.selectedListed, true);
  });

  it("新入口在已审查 SHA-256 表内且哈希一致时放行", () => {
    const files = { "dev/main.js": "// reviewed dev build" };
    const result = evaluateGate({
      ...base,
      additionalConditions: ["development"],
      allowedConditions: ["development"],
      reviewed: { "./dev/main.js": sha(files["dev/main.js"]) },
      readFile: memoryFs(files),
    });

    assert.equal(result.decision, "allow");
    assert.equal(result.reason, "allow-reviewed-entry");
    assert.equal(result.review.listed, true);
    assert.equal(result.review.hashOk, true);
    assert.equal(result.review.actual, sha256Hex(new TextEncoder().encode(files["dev/main.js"])));
  });

  it("允许集合外的附加条件视为未传入，入口不变直接放行", () => {
    const result = evaluateGate({
      ...base,
      additionalConditions: ["deno", "development"],
      allowedConditions: ["bun"],
      manifest: manifestAll,
    });

    assert.deepEqual(result.activeAdditionalConditions, []);
    assert.deepEqual(result.ignoredConditions, ["deno", "development"]);
    assert.equal(result.decision, "allow");
    assert.equal(result.reason, "allow-same-entry");
    assert.equal(result.divergence.changed, false);
    assert.equal(result.divergence.condition, null);
    assert.equal(result.selectedEntry, "./dist/main.js");
  });

  it("null 目标封锁，带附加条件时拒绝", () => {
    const result = evaluateGate({
      ...base,
      exports: blockedExports,
      additionalConditions: ["development"],
      allowedConditions: ["development"],
    });

    assert.equal(result.augmented.blocked, true);
    assert.equal(result.augmented.found, false);
    assert.equal(result.decision, "deny");
    assert.equal(result.reason, "deny-blocked");
    assert.equal(result.selectedEntry, null);
    assert.deepEqual(result.divergence.key, [".", "development"]);
    // 基线未命中 development，仍解析到正常入口
    assert.equal(result.baseline.target, "./dist/main.js");
  });

  it("被选中的入口不在包文件清单内时拒绝（即使已登记审查）", () => {
    const files = { "dev/main.js": "// phantom file" };
    const result = evaluateGate({
      ...base,
      manifest: manifestWithoutDev,
      additionalConditions: ["development"],
      allowedConditions: ["development"],
      reviewed: { "dev/main.js": sha(files["dev/main.js"]) },
      readFile: memoryFs(files),
    });

    assert.equal(result.decision, "deny");
    assert.equal(result.reason, "deny-missing-from-manifest");
    assert.equal(result.manifest.selectedListed, false);
  });

  it("已登记但实际哈希不一致时拒绝", () => {
    const files = { "dev/main.js": "// tampered build" };
    const result = evaluateGate({
      ...base,
      additionalConditions: ["development"],
      allowedConditions: ["development"],
      reviewed: { "dev/main.js": sha("// original build") },
      readFile: memoryFs(files),
    });

    assert.equal(result.decision, "deny");
    assert.equal(result.reason, "deny-hash-mismatch");
    assert.equal(result.review.hashOk, false);
  });

  it("无法读取入口内容时拒绝而不是静默放行", () => {
    const result = evaluateGate({
      ...base,
      additionalConditions: ["development"],
      allowedConditions: ["development"],
      reviewed: { "dev/main.js": sha("something") },
    });

    assert.equal(result.decision, "deny");
    assert.equal(result.reason, "deny-hash-unreadable");
  });

  it("两次入口相同则无需审查表也放行", () => {
    const result = evaluateGate({
      ...base,
      additionalConditions: ["deno"],
      allowedConditions: ["development"],
    });

    assert.deepEqual(result.ignoredConditions, ["deno"]);
    assert.equal(result.decision, "allow");
    assert.equal(result.reason, "allow-same-entry");
  });

  it("无法解析的说明符拒绝", () => {
    const result = evaluateGate({
      ...base,
      specifier: "victim-pkg/missing",
      additionalConditions: ["development"],
      allowedConditions: ["development"],
    });

    assert.equal(result.decision, "deny");
    assert.equal(result.reason, "deny-unresolvable");
    assert.equal(result.selectedEntry, null);
  });

  it("多个附加条件均能改变入口时标记为联合归因", () => {
    const exportsField: ExportsField = {
      ".": {
        development: "./dev/main.js",
        deno: "./dev/main.js",
        default: "./dist/main.cjs",
      },
    };
    const result = evaluateGate({
      ...base,
      exports: exportsField,
      additionalConditions: ["development", "deno"],
      allowedConditions: ["development", "deno"],
    });

    assert.equal(result.divergence.changed, true);
    assert.equal(result.divergence.attribution, "combined");
    assert.equal(result.decision, "deny");
    assert.equal(result.divergence.probes.length, 2);
    assert.ok(result.divergence.probes.every((probe) => probe.diverges));
  });
});
