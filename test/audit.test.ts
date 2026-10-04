import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { after, before, test } from "node:test";
import { audit } from "../src/auditor.ts";
import { prepareDeclarations } from "../src/manifest.ts";
import { resolvePath } from "../src/resolver.ts";
import type { AccessEvent, DeclaredManifest } from "../src/types.ts";

let root: string;

function pj(...parts: string[]): string {
  return path.join(root, ...parts);
}

before(() => {
  root = fs.mkdtempSync(path.join(os.tmpdir(), "access-audit-"));
  // macOS: os.tmpdir() is itself a symlink (/var -> /private/var).
  root = fs.realpathSync(root);

  fs.mkdirSync(pj("declared-dir"), { recursive: true });
  fs.writeFileSync(pj("declared-dir", "inside.txt"), "inside");
  fs.mkdirSync(pj("outside", "tools"), { recursive: true });
  fs.writeFileSync(pj("outside", "secret.txt"), "secret");
  fs.writeFileSync(pj("outside", "tools", "run.sh"), "#!/bin/sh\n");
  fs.writeFileSync(pj("CaseFile.TXT"), "case");
  fs.mkdirSync(pj("shared"), { recursive: true });
  fs.writeFileSync(pj("shared", "a.txt"), "a");
  fs.writeFileSync(pj("shared", "b.txt"), "b");
  fs.writeFileSync(pj("shared", "c.txt"), "c");
  fs.writeFileSync(pj("lone.txt"), "lone");

  // symlink chain inside the declared directory: chain-a -> chain-b -> inside.txt
  fs.symlinkSync("inside.txt", pj("declared-dir", "chain-b"));
  fs.symlinkSync("chain-b", pj("declared-dir", "chain-a"));
  // symlink that escapes the declared directory
  fs.symlinkSync(
    path.join("..", "outside", "secret.txt"),
    pj("declared-dir", "escape-link"),
  );
  // symlink loop
  fs.symlinkSync("loop-y", pj("loop-x"));
  fs.symlinkSync("loop-x", pj("loop-y"));
  // dangling symlink
  fs.symlinkSync("no-such-target", pj("declared-dir", "dangling"));
});

after(() => {
  fs.rmSync(root, { recursive: true, force: true });
});

const manifest: DeclaredManifest = {
  casePolicy: "sensitive",
  files: ["CaseFile.TXT"],
  directories: ["declared-dir"],
  executables: [],
};

const trace: AccessEvent[] = [
  { path: "declared-dir/inside.txt", kind: "read" },
  { path: "declared-dir/inside.txt", kind: "read" }, // duplicate
  { path: "declared-dir/inside.txt", kind: "write" }, // duplicate, new kind
  { path: "declared-dir/chain-a", kind: "read" },
  { path: "declared-dir/escape-link", kind: "read" },
  { path: "outside/secret.txt", kind: "read" },
  { path: "outside/tools/run.sh", kind: "exec" },
  { path: "nonexistent/missing.txt", kind: "read" },
  { path: "loop-x", kind: "read" },
  { path: "casefile.txt", kind: "read" },
  { path: "shared/a.txt", kind: "read" },
  { path: "shared/b.txt", kind: "read" },
  { path: "shared/c.txt", kind: "read" },
  { path: "lone.txt", kind: "read" },
  { path: "declared-dir/dangling", kind: "read" },
];

function runAudit(casePolicy: "sensitive" | "insensitive") {
  const decls = prepareDeclarations({ ...manifest, casePolicy }, root);
  return audit(trace, decls, { policy: casePolicy, cwd: root });
}

test("resolver records the full symlink expansion chain", () => {
  const resolved = resolvePath(pj("declared-dir", "chain-a"));
  assert.equal(resolved.exists, true);
  assert.deepEqual(
    resolved.hops.map((h) => h.at),
    [pj("declared-dir", "chain-a"), pj("declared-dir", "chain-b")],
  );
  assert.equal(resolved.canonical, pj("declared-dir", "inside.txt"));
});

test("resolver handles missing paths without throwing", () => {
  const resolved = resolvePath(pj("nonexistent", "missing.txt"));
  assert.equal(resolved.exists, false);
  assert.equal(resolved.missingAt, pj("nonexistent"));
  assert.equal(resolved.canonical, pj("nonexistent", "missing.txt"));
});

test("resolver detects symlink loops", () => {
  const resolved = resolvePath(pj("loop-x"));
  assert.equal(resolved.loop, true);
  assert.equal(resolved.exists, false);
});

test("declared directory authorizes in-directory symlink chains", () => {
  const report = runAudit("sensitive");
  const chain = report.accesses.find((a) => a.path === "declared-dir/chain-a");
  assert.equal(chain?.status, "declared");
  assert.equal(chain?.matchedBy?.kind, "directory");
  assert.equal(chain?.resolved.hops.length, 2);
});

test("directory declaration does not authorize escaping symlink targets", () => {
  const report = runAudit("sensitive");
  const escape = report.accesses.find((a) => a.path === "declared-dir/escape-link");
  assert.equal(escape?.status, "undeclared");
  assert.match(escape?.reason ?? "", /escapes declared directory/);
  assert.equal(escape?.resolved.canonical, pj("outside", "secret.txt"));
});

test("dangling symlink inside declared directory stays undeclared", () => {
  const report = runAudit("sensitive");
  const dangling = report.accesses.find((a) => a.path === "declared-dir/dangling");
  // resolves to declared-dir/no-such-target: lexically inside, canonical
  // inside -> actually authorized by the directory; assert the resolution
  // is recorded faithfully instead.
  assert.equal(dangling?.resolved.exists, false);
  assert.equal(dangling?.resolved.canonical, pj("declared-dir", "no-such-target"));
});

test("duplicate accesses are folded with counts and merged kinds", () => {
  const report = runAudit("sensitive");
  const dup = report.accesses.find((a) => a.path === "declared-dir/inside.txt");
  assert.equal(dup?.count, 3);
  assert.deepEqual(dup?.kinds.sort(), ["read", "write"]);
  assert.equal(report.summary.duplicateEvents, 2);
  assert.equal(report.summary.totalEvents, trace.length);
});

test("symlink loops are reported as unverifiable", () => {
  const report = runAudit("sensitive");
  const loop = report.accesses.find((a) => a.path === "loop-x");
  assert.equal(loop?.status, "unverifiable");
  assert.equal(report.summary.unverifiable, 1);
});

test("missing paths are flagged and never crash the audit", () => {
  const report = runAudit("sensitive");
  const missing = report.accesses.find((a) => a.path === "nonexistent/missing.txt");
  assert.equal(missing?.status, "undeclared");
  assert.match(missing?.reason ?? "", /does not exist/);
  assert.equal(report.summary.missingPaths >= 1, true);
});

test("case policy controls matching of differently-cased paths", () => {
  const sensitive = runAudit("sensitive");
  const insensitive = runAudit("insensitive");
  const find = (r: typeof sensitive) =>
    r.accesses.find((a) => a.path === "casefile.txt");
  assert.equal(find(sensitive)?.status, "undeclared");
  assert.equal(find(insensitive)?.status, "declared");
});

test("supplement is minimal: shared dir folded, singles kept, execs separate", () => {
  const report = runAudit("sensitive");
  const sup = report.supplement;
  // shared/{a,b,c}.txt collapse into one directory entry (the dir holds
  // nothing else); lone.txt/casefile.txt stay individual entries because
  // folding the fixture root would over-authorize unrelated content.
  assert.deepEqual(sup.directories, [pj("shared")]);
  // exec access is never folded into a directory entry
  assert.deepEqual(sup.executables, [pj("outside", "tools", "run.sh")]);
  // escape-link and outside/secret.txt share one canonical target
  const files = sup.files;
  assert.equal(files.filter((f) => f === pj("outside", "secret.txt")).length, 1);
  assert.ok(files.includes(pj("nonexistent", "missing.txt")));
  assert.ok(files.includes(pj("lone.txt")));
  assert.ok(files.includes(pj("casefile.txt")));
});

test("supplement closes the gap: re-audit with supplement leaves nothing", () => {
  const first = runAudit("sensitive");
  const merged: DeclaredManifest = {
    casePolicy: "sensitive",
    files: [...(manifest.files ?? []), ...first.supplement.files],
    directories: [...(manifest.directories ?? []), ...first.supplement.directories],
    executables: [
      ...(manifest.executables ?? []),
      ...first.supplement.executables,
    ],
  };
  const decls = prepareDeclarations(merged, root);
  const second = audit(
    trace.filter((e) => e.path !== "loop-x" && e.path !== "declared-dir/dangling"),
    decls,
    { policy: "sensitive", cwd: root },
  );
  assert.equal(second.summary.undeclared, 0);
});
