import { test } from "node:test";
import assert from "node:assert/strict";
import { diffSnapshots } from "../src/diff.ts";
import { compressPaths } from "../src/compress.ts";
import { matchPattern, specificity } from "../src/pattern.ts";
import type {
  OrgPolicy,
  Resource,
  Snapshot,
  UserException,
} from "../src/types.ts";

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

const CATALOG: Resource[] = [
  { type: "doc", path: "docs/public/a" },
  { type: "doc", path: "docs/public/b" },
  { type: "doc", path: "docs/secret/hr" },
  { type: "doc", path: "docs/secret/salaries" },
  { type: "log", path: "logs/2026/01" },
  { type: "log", path: "logs/2026/02" },
];

function makeSnapshot(opts: {
  memberNode?: string;
  nodeNames?: Record<string, string>;
  policies?: OrgPolicy[];
  exceptions?: UserException[];
}): Snapshot {
  const names = {
    corp: "Corp",
    eng: "Eng",
    teamA: "TeamA",
    ...opts.nodeNames,
  };
  return {
    tree: {
      nodes: [
        { id: "corp", name: names.corp, parentId: null },
        { id: "eng", name: names.eng, parentId: "corp" },
        { id: "teamA", name: names.teamA, parentId: "eng" },
      ],
      members: [{ userId: "alice", nodeId: opts.memberNode ?? "teamA" }],
    },
    policies: opts.policies ?? [],
    exceptions: opts.exceptions ?? [],
  };
}

const readAllDocs: OrgPolicy = {
  id: "p1",
  nodeId: "corp",
  effect: "allow",
  resourceType: "doc",
  action: "read",
  pathPattern: "docs/*",
};

const denySecret: OrgPolicy = {
  id: "p3",
  nodeId: "eng",
  effect: "deny",
  resourceType: "doc",
  action: "read",
  pathPattern: "docs/secret/*",
};

// ---------------------------------------------------------------------------
// pattern matching
// ---------------------------------------------------------------------------

test("matchPattern: exact, prefix wildcard, full wildcard", () => {
  assert.ok(matchPattern("*", "anything/at/all"));
  assert.ok(matchPattern("a/b/*", "a/b"));
  assert.ok(matchPattern("a/b/*", "a/b/c/d"));
  assert.ok(!matchPattern("a/b/*", "a/bc"));
  assert.ok(matchPattern("a/b/c", "a/b/c"));
  assert.ok(!matchPattern("a/b/c", "a/b"));
});

test("specificity: concrete segments beat wildcards", () => {
  assert.ok(specificity("a/b/c") > specificity("a/b/*"));
  assert.ok(specificity("a/b/*") > specificity("*"));
});

// ---------------------------------------------------------------------------
// inheritance, exceptions, shortest source chain
// ---------------------------------------------------------------------------

test("permissions are inherited from ancestors with the shortest chain reported", () => {
  const snapshot = makeSnapshot({ policies: [readAllDocs] });
  const report = diffSnapshots(makeSnapshot({}), snapshot, CATALOG);
  const added = report.users[0].byResourceType.doc.added;
  assert.equal(added.length, 1);
  assert.deepEqual(added[0].paths, ["*"]); // whole doc catalog, compressed
  assert.deepEqual(added[0].source.hops, [
    "policy:p1(allow read docs/*)",
    "org:Corp",
    "org:Eng",
    "org:TeamA",
    "user:alice",
  ]);
});

test("closer node wins, producing the shortest source chain", () => {
  const closerAllow: OrgPolicy = {
    id: "p9",
    nodeId: "eng",
    effect: "allow",
    resourceType: "doc",
    action: "read",
    pathPattern: "docs/*",
  };
  const snapshot = makeSnapshot({ policies: [readAllDocs, closerAllow] });
  const report = diffSnapshots(makeSnapshot({}), snapshot, CATALOG);
  const added = report.users[0].byResourceType.doc.added;
  assert.equal(added.length, 1);
  // decided by p9 on Eng, not by p1 on Corp: chain is shorter
  assert.deepEqual(added[0].source.hops, [
    "policy:p9(allow read docs/*)",
    "org:Eng",
    "org:TeamA",
    "user:alice",
  ]);
});

test("explicit exception overrides inherited deny", () => {
  const exception: UserException = {
    id: "e1",
    userId: "alice",
    effect: "allow",
    resourceType: "doc",
    action: "read",
    pathPattern: "docs/secret/hr",
  };
  const snapshot = makeSnapshot({
    policies: [readAllDocs, denySecret],
    exceptions: [exception],
  });
  const report = diffSnapshots(makeSnapshot({}), snapshot, CATALOG);
  const added = report.users[0].byResourceType.doc.added;
  const secretGroup = added.find((g) => g.paths.includes("docs/secret/hr"));
  assert.ok(secretGroup, "exception-granted doc present");
  assert.deepEqual(secretGroup.source.hops, [
    "exception:e1(allow read docs/secret/hr)",
    "user:alice",
  ]);
  // the other secret doc stays denied: it must not appear anywhere
  const allPaths = [
    ...added,
    ...report.users[0].byResourceType.doc.unchanged,
  ].flatMap((g) => g.paths);
  assert.ok(!allPaths.some((p) => p.includes("salaries")));
});

// ---------------------------------------------------------------------------
// rename is not a permission change
// ---------------------------------------------------------------------------

test("renaming an org is not a permission change", () => {
  const before = makeSnapshot({
    policies: [readAllDocs],
    nodeNames: { eng: "Eng" },
  });
  const after = makeSnapshot({
    policies: [readAllDocs],
    nodeNames: { eng: "Engineering", teamA: "Team Alpha" },
  });
  const report = diffSnapshots(before, after, CATALOG);
  const user = report.users[0];
  assert.ok(user.clean, "no added/lost after pure rename");
  const doc = user.byResourceType.doc;
  assert.deepEqual(doc.added, []);
  assert.deepEqual(doc.lost, []);
  assert.deepEqual(doc.unchanged[0].paths, ["*"]);
  // chain reflects the new names, but nothing is flagged as changed
  assert.ok(doc.unchanged[0].source.hops.includes("org:Engineering"));
});

// ---------------------------------------------------------------------------
// deleting exceptions must not be missed
// ---------------------------------------------------------------------------

test("deleting an allow exception reports the lost permission", () => {
  const exception: UserException = {
    id: "e1",
    userId: "alice",
    effect: "allow",
    resourceType: "doc",
    action: "read",
    pathPattern: "docs/secret/hr",
  };
  const before = makeSnapshot({
    policies: [readAllDocs, denySecret],
    exceptions: [exception],
  });
  const after = makeSnapshot({
    policies: [readAllDocs, denySecret],
    exceptions: [],
  });
  const report = diffSnapshots(before, after, CATALOG);
  const lost = report.users[0].byResourceType.doc.lost;
  assert.equal(lost.length, 1);
  assert.deepEqual(lost[0].paths, ["docs/secret/hr"]);
  assert.equal(lost[0].source.ruleKind, "exception");
  assert.equal(lost[0].source.ruleId, "e1");
});

test("deleting a deny exception restores the inherited permission", () => {
  const denyException: UserException = {
    id: "e2",
    userId: "alice",
    effect: "deny",
    resourceType: "doc",
    action: "read",
    pathPattern: "docs/public/b",
  };
  const before = makeSnapshot({
    policies: [readAllDocs],
    exceptions: [denyException],
  });
  const after = makeSnapshot({ policies: [readAllDocs], exceptions: [] });
  const report = diffSnapshots(before, after, CATALOG);
  const doc = report.users[0].byResourceType.doc;
  assert.equal(doc.lost.length, 0);
  assert.equal(doc.added.length, 1);
  assert.deepEqual(doc.added[0].paths, ["docs/public/b"]);
  // regained via inheritance: source chain points at the ancestor policy
  assert.equal(doc.added[0].source.ruleKind, "policy");
  assert.equal(doc.added[0].source.ruleId, "p1");
});

// ---------------------------------------------------------------------------
// migration between subtrees
// ---------------------------------------------------------------------------

test("moving a user to a different subtree loses old and gains new grants", () => {
  const engLogs: OrgPolicy = {
    id: "p4",
    nodeId: "eng",
    effect: "allow",
    resourceType: "log",
    action: "read",
    pathPattern: "logs/*",
  };
  const before = makeSnapshot({ memberNode: "teamA", policies: [engLogs] });
  // move alice out from under eng: re-parent her to corp directly
  const after = makeSnapshot({ memberNode: "corp", policies: [engLogs] });
  const report = diffSnapshots(before, after, CATALOG);
  const log = report.users[0].byResourceType.log;
  assert.deepEqual(log.added, []);
  assert.deepEqual(log.lost[0].paths, ["*"]); // entire log catalog

  const reverse = diffSnapshots(after, before, CATALOG);
  assert.deepEqual(reverse.users[0].byResourceType.log.added[0].paths, [
    "*",
  ]);
});

// ---------------------------------------------------------------------------
// path compression
// ---------------------------------------------------------------------------

test("compressPaths collapses fully covered subtrees only", () => {
  const catalog = CATALOG.filter((r) => r.type === "doc").map((r) => r.path);
  assert.deepEqual(
    compressPaths(["docs/public/a", "docs/public/b"], catalog),
    ["docs/public/*"],
  );
  assert.deepEqual(
    compressPaths(["docs/public/a"], catalog),
    ["docs/public/a"],
  );
  assert.deepEqual(compressPaths(catalog, catalog), ["*"]);
  assert.deepEqual(
    compressPaths([...catalog, "logs/2026/01", "logs/2026/02"], [
      ...catalog,
      "logs/2026/01",
      "logs/2026/02",
    ]),
    ["*"],
  );
  // partial coverage never over-claims
  assert.deepEqual(
    compressPaths(["docs/public/a", "docs/secret/hr"], catalog),
    ["docs/public/a", "docs/secret/hr"],
  );
});

// ---------------------------------------------------------------------------
// unchanged permissions are reported, per resource type
// ---------------------------------------------------------------------------

test("unchanged permissions are reported per resource type", () => {
  const write: OrgPolicy = {
    id: "p2",
    nodeId: "eng",
    effect: "allow",
    resourceType: "doc",
    action: "write",
    pathPattern: "docs/public/*",
  };
  const before = makeSnapshot({ policies: [readAllDocs] });
  const after = makeSnapshot({ policies: [readAllDocs, write] });
  const report = diffSnapshots(before, after, CATALOG);
  const doc = report.users[0].byResourceType.doc;
  assert.deepEqual(doc.unchanged[0].paths, ["*"]);
  assert.equal(doc.unchanged[0].action, "read");
  assert.deepEqual(doc.added[0].paths, ["docs/public/*"]);
  assert.equal(doc.added[0].action, "write");
  assert.equal(doc.lost.length, 0);
});
