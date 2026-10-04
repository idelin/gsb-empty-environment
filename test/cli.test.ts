import { test } from "node:test";
import * as assert from "node:assert/strict";
import { execFileSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import { makeFixture } from "./helpers";

const CLI = path.join(__dirname, "..", "src", "cli.js");

function runCli(
  args: string[]
): { code: number; stdout: string; stderr: string } {
  try {
    const stdout = execFileSync(process.execPath, [CLI, ...args], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    return { code: 0, stdout, stderr: "" };
  } catch (err) {
    const e = err as { status: number; stdout: string; stderr: string };
    return { code: e.status, stdout: e.stdout, stderr: e.stderr };
  }
}

test("check exits 1 and writes a minimal supplement manifest", () => {
  const fx = makeFixture();
  try {
    const manifest = path.join(fx.root, "manifest.json");
    const workload = path.join(fx.root, "workload.json");
    const report = path.join(fx.root, "report.json");
    const supplement = path.join(fx.root, "supplement.json");
    fs.writeFileSync(
      manifest,
      JSON.stringify({
        version: 1,
        declarations: [{ path: "declared", type: "dir" }],
      })
    );
    fs.writeFileSync(
      workload,
      JSON.stringify({
        ops: [
          { op: "readFile", path: "declared/keep.txt" },
          { op: "readFile", path: "declared/escape-link" },
          { op: "readFile", path: "declared/escape-link" },
          { op: "stat", path: "missing.txt" },
        ],
      })
    );
    const res = runCli([
      "check",
      "--manifest",
      manifest,
      "--workload",
      workload,
      "--base",
      fx.root,
      "--case",
      "sensitive",
      "--report",
      report,
      "--supplement-out",
      supplement,
    ]);
    assert.equal(res.code, 1, res.stderr);

    const parsedReport = JSON.parse(fs.readFileSync(report, "utf8"));
    assert.equal(parsedReport.summary.totalAccesses, 4);
    assert.equal(parsedReport.summary.uniqueAccesses, 3);
    assert.equal(parsedReport.summary.declared, 1);
    assert.equal(parsedReport.summary.symlinkEscapes, 1);
    assert.equal(parsedReport.summary.undeclared, 1);

    const parsedSupplement = JSON.parse(fs.readFileSync(supplement, "utf8"));
    assert.deepEqual(
      parsedSupplement.declarations.map((d: { path: string }) => d.path).sort(),
      [
        path.join(fx.root, "missing.txt"),
        path.join(fx.realRoot, "outside", "secret.txt"),
      ].sort()
    );
  } finally {
    fx.cleanup();
  }
});

test("check exits 0 when everything is declared", () => {
  const fx = makeFixture();
  try {
    const manifest = path.join(fx.root, "manifest.json");
    const workload = path.join(fx.root, "workload.json");
    fs.writeFileSync(
      manifest,
      JSON.stringify({
        declarations: [{ path: "declared", type: "dir" }],
      })
    );
    fs.writeFileSync(
      workload,
      JSON.stringify({
        ops: [
          { op: "readFile", path: "declared/keep.txt" },
          { op: "readFile", path: "declared/inner-link" },
        ],
      })
    );
    const res = runCli([
      "check",
      "--manifest",
      manifest,
      "--workload",
      workload,
      "--base",
      fx.root,
      "--case",
      "sensitive",
    ]);
    assert.equal(res.code, 0, res.stderr);
    const parsed = JSON.parse(res.stdout);
    assert.equal(parsed.summary.undeclared, 0);
    assert.equal(parsed.summary.symlinkEscapes, 0);
  } finally {
    fx.cleanup();
  }
});
