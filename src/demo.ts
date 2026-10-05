import { diffSnapshots } from "./diff.ts";
import type { Resource, Snapshot } from "./types.ts";

// ---------------------------------------------------------------------------
// Scenario: the company restructures. "Eng" (id=eng) is renamed to
// "Engineering", alice moves from "TeamA" up to "Engineering", a new
// policy grants logs access, and two user exceptions are deleted.
// ---------------------------------------------------------------------------

const before: Snapshot = {
  tree: {
    nodes: [
      { id: "corp", name: "Corp", parentId: null },
      { id: "eng", name: "Eng", parentId: "corp" },
      { id: "teamA", name: "TeamA", parentId: "eng" },
    ],
    members: [
      { userId: "alice", nodeId: "teamA" },
      { userId: "bob", nodeId: "eng" },
    ],
  },
  policies: [
    { id: "p1", nodeId: "corp", effect: "allow", resourceType: "doc", action: "read", pathPattern: "docs/*" },
    { id: "p2", nodeId: "eng", effect: "allow", resourceType: "doc", action: "write", pathPattern: "docs/public/*" },
    { id: "p3", nodeId: "eng", effect: "deny", resourceType: "doc", action: "read", pathPattern: "docs/secret/*" },
  ],
  exceptions: [
    // alice may read one secret doc explicitly; removed after migration
    { id: "e1", userId: "alice", effect: "allow", resourceType: "doc", action: "read", pathPattern: "docs/secret/hr" },
    // bob is blocked from one public doc; removed after migration
    { id: "e2", userId: "bob", effect: "deny", resourceType: "doc", action: "write", pathPattern: "docs/public/b" },
  ],
};

const after: Snapshot = {
  tree: {
    nodes: [
      { id: "corp", name: "Corp", parentId: null },
      { id: "eng", name: "Engineering", parentId: "corp" }, // renamed, same id
      { id: "teamA", name: "TeamA", parentId: "eng" },
    ],
    members: [
      { userId: "alice", nodeId: "eng" }, // moved up one level
      { userId: "bob", nodeId: "eng" },
    ],
  },
  policies: [
    { id: "p1", nodeId: "corp", effect: "allow", resourceType: "doc", action: "read", pathPattern: "docs/*" },
    { id: "p2", nodeId: "eng", effect: "allow", resourceType: "doc", action: "write", pathPattern: "docs/public/*" },
    { id: "p3", nodeId: "eng", effect: "deny", resourceType: "doc", action: "read", pathPattern: "docs/secret/*" },
    { id: "p4", nodeId: "eng", effect: "allow", resourceType: "log", action: "read", pathPattern: "logs/*" },
  ],
  exceptions: [], // e1 and e2 deleted
};

const catalog: Resource[] = [
  { type: "doc", path: "docs/public/a" },
  { type: "doc", path: "docs/public/b" },
  { type: "doc", path: "docs/secret/hr" },
  { type: "doc", path: "docs/secret/salaries" },
  { type: "log", path: "logs/2026/01" },
  { type: "log", path: "logs/2026/02" },
];

const report = diffSnapshots(before, after, catalog);

for (const user of report.users) {
  console.log(`\n=== ${user.userId} ${user.clean ? "(no permission changes)" : ""}`);
  for (const [resourceType, diff] of Object.entries(user.byResourceType)) {
    console.log(`  [${resourceType}]`);
    for (const [label, groups] of [
      ["added", diff.added],
      ["lost", diff.lost],
      ["unchanged", diff.unchanged],
    ] as const) {
      for (const group of groups) {
        console.log(
          `    ${label.padEnd(9)} ${group.action} ${group.paths.join(", ")}`,
        );
        console.log(`      source: ${group.source.hops.join(" -> ")}`);
      }
    }
  }
}
