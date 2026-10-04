import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as path from "path";
import { audit, minimizeSupplement } from "../src/auditor";
import { runWorkload } from "../src/tracer";
import { DeclEntry } from "../src/types";
import { makeFixture } from "./helpers";

function check(
  root: string,
  declarations: DeclEntry[],
  ops: { op: "readFile" | "readdir" | "stat" | "exists"; path: string }[],
  casePolicy: "sensitive" | "insensitive" = "sensitive"
) {
  const log = runWorkload({ ops }, root);
  const decls = declarations.map((d) => ({
    recursive: true,
    ...d,
    path: path.isAbsolute(d.path) ? d.path : path.join(root, d.path),
  }));
  return audit(log, decls, { casePolicy, probeDir: root });
}

test("exact file declaration authorizes the access", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared/keep.txt", type: "file" }],
      [{ op: "readFile", path: "declared/keep.txt" }]
    );
    assert.equal(report.summary.declared, 1);
    assert.equal(report.summary.undeclared, 0);
    assert.equal(report.supplement.length, 0);
  } finally {
    fx.cleanup();
  }
});

test("dir declaration covers nested files recursively", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [
        { op: "readFile", path: "declared/keep.txt" },
        { op: "readFile", path: "declared/sub/deep.txt" },
      ]
    );
    assert.equal(report.summary.declared, 2);
  } finally {
    fx.cleanup();
  }
});

test("non-recursive dir declaration covers only direct children", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir", recursive: false }],
      [
        { op: "readFile", path: "declared/keep.txt" },
        { op: "readFile", path: "declared/sub/deep.txt" },
      ]
    );
    assert.equal(report.summary.declared, 1);
    assert.equal(report.summary.undeclared, 1);
    const deep = report.accesses.find((a) => a.path.endsWith("deep.txt"));
    assert.equal(deep?.status, "undeclared");
  } finally {
    fx.cleanup();
  }
});

test("dir declaration does NOT authorize symlink targets escaping the dir", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [{ op: "readFile", path: "declared/escape-link" }]
    );
    assert.equal(report.summary.symlinkEscapes, 1);
    const access = report.accesses[0];
    assert.equal(access.status, "symlink-escape");
    assert.ok(access.reason?.includes("escapes declared directory"));
    // Minimal supplement must name the real target outside the dir.
    assert.deepEqual(
      report.supplement.map((s) => s.path),
      [path.join(fx.realRoot, "outside", "secret.txt")]
    );
    assert.equal(report.supplement[0].type, "file");
  } finally {
    fx.cleanup();
  }
});

test("escape through a symlinked directory component is caught", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [{ op: "readFile", path: "declared/dir-link/secret.txt" }]
    );
    assert.equal(report.summary.symlinkEscapes, 1);
    assert.equal(
      report.supplement[0].path,
      path.join(fx.realRoot, "outside", "secret.txt")
    );
  } finally {
    fx.cleanup();
  }
});

test("symlink staying inside the declared dir is authorized", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [{ op: "readFile", path: "declared/inner-link" }]
    );
    assert.equal(report.summary.declared, 1);
    const last = report.accesses[0].chain[report.accesses[0].chain.length - 1];
    assert.equal(last.at, path.join(fx.realRoot, "declared", "inner-link"));
  } finally {
    fx.cleanup();
  }
});

test("access via undeclared symlink is authorized when the real target is declared", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "outside/secret.txt", type: "file" }],
      [{ op: "readFile", path: "root-link" }]
    );
    assert.equal(report.summary.declared, 1);
  } finally {
    fx.cleanup();
  }
});

test("non-existent path inside declared dir is authorized; outside is not", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [
        { op: "stat", path: "declared/new-file.txt" },
        { op: "stat", path: "elsewhere/new-file.txt" },
      ]
    );
    assert.equal(report.summary.missing, 2);
    const inside = report.accesses.find((a) => a.path.includes("declared"));
    const outside = report.accesses.find((a) => a.path.includes("elsewhere"));
    assert.equal(inside?.status, "declared");
    assert.equal(outside?.status, "undeclared");
    assert.equal(outside?.exists, false);
    // Supplement uses the lexical path since nothing was resolved.
    assert.deepEqual(
      report.supplement.map((s) => s.path),
      [path.join(fx.root, "elsewhere", "new-file.txt")]
    );
  } finally {
    fx.cleanup();
  }
});

test("case-insensitive policy matches different casing; sensitive does not", () => {
  const fx = makeFixture();
  try {
    const insensitive = check(
      fx.root,
      [{ path: "declared/keep.txt", type: "file" }],
      [{ op: "readFile", path: "declared/KEEP.TXT" }],
      "insensitive"
    );
    assert.equal(insensitive.summary.declared, 1);

    const sensitive = check(
      fx.root,
      [{ path: "declared/keep.txt", type: "file" }],
      [{ op: "readFile", path: "declared/KEEP.TXT" }],
      "sensitive"
    );
    assert.equal(sensitive.summary.declared, 0);
    assert.equal(sensitive.summary.undeclared, 1);
  } finally {
    fx.cleanup();
  }
});

test("duplicate accesses are collapsed with a count", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [
        { op: "readFile", path: "declared/keep.txt" },
        { op: "readFile", path: "declared/keep.txt" },
        { op: "readFile", path: "declared/keep.txt" },
      ]
    );
    assert.equal(report.summary.totalAccesses, 3);
    assert.equal(report.summary.uniqueAccesses, 1);
    assert.equal(report.accesses[0].count, 3);
  } finally {
    fx.cleanup();
  }
});

test("symlink loops surface as errors and undeclared accesses", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [{ op: "readFile", path: "loop-a" }]
    );
    assert.equal(report.accesses[0].error, "ELOOP");
    assert.equal(report.accesses[0].status, "undeclared");
  } finally {
    fx.cleanup();
  }
});

test("readdir on undeclared dir produces a dir supplement entry", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [{ op: "readdir", path: "outside" }]
    );
    assert.equal(report.summary.undeclared, 1);
    assert.deepEqual(report.supplement, [
      {
        path: path.join(fx.root, "outside"),
        type: "dir",
        reason: "no declaration covers this path",
      },
    ]);
  } finally {
    fx.cleanup();
  }
});

test("minimizeSupplement dedupes and drops files covered by supplement dirs", () => {
  const entries = minimizeSupplement(
    [
      { path: "/a/b", type: "dir", reason: "r" },
      { path: "/a/b/c.txt", type: "file", reason: "r" },
      { path: "/a/b/c.txt", type: "file", reason: "r" },
      { path: "/a/b", type: "dir", reason: "r" },
      { path: "/a/b/esc.txt", type: "file", reason: "symlink escape: x" },
    ],
    "sensitive"
  );
  assert.deepEqual(entries, [
    { path: "/a/b", type: "dir", reason: "r" },
    { path: "/a/b/esc.txt", type: "file", reason: "symlink escape: x" },
  ]);
});

test("two escapes to the same target yield one supplement entry", () => {
  const fx = makeFixture();
  try {
    const report = check(
      fx.root,
      [{ path: "declared", type: "dir" }],
      [
        { op: "readFile", path: "declared/escape-link" },
        { op: "readFile", path: "declared/dir-link/secret.txt" },
      ]
    );
    assert.equal(report.summary.symlinkEscapes, 2);
    assert.equal(report.supplement.length, 1);
    assert.equal(
      report.supplement[0].path,
      path.join(fx.realRoot, "outside", "secret.txt")
    );
  } finally {
    fx.cleanup();
  }
});
