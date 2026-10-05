import { migrateCoverage } from "../src/migrate";
import {
  MappingRule,
  NewCoverageModel,
  NewStatement,
  OldCoverageData,
  OldStatement,
  SourceRange,
} from "../src/types";

let failures = 0;
let checks = 0;

function check(name: string, cond: boolean): void {
  checks++;
  if (cond) {
    console.log("ok   " + name);
  } else {
    failures++;
    console.log("FAIL " + name);
  }
}

function eq(name: string, actual: unknown, expected: unknown): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  check(name + " (expected " + e + ", got " + a + ")", a === e);
}

function range(sl: number, sc: number, el: number, ec: number): SourceRange {
  return { start: { line: sl, column: sc }, end: { line: el, column: ec } };
}

function oldStmt(
  id: string,
  file: string,
  r: SourceRange,
  hits: number,
  text: string
): OldStatement {
  const s: OldStatement = { id: id, file: file, range: r, hits: hits };
  if (text) {
    s.text = text;
  }
  return s;
}

function newStmt(id: string, file: string, r: SourceRange, text: string): NewStatement {
  const s: NewStatement = { id: id, file: file, range: r };
  if (text) {
    s.text = text;
  }
  return s;
}

function oldData(statements: OldStatement[]): OldCoverageData {
  return { statements: statements };
}

function newData(statements: NewStatement[]): NewCoverageModel {
  return { statements: statements };
}

// 1. Identical range auto-match: statement did not move, coverage inherited.
{
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(1, 0, 1, 20), 5, "foo()")]),
    newData([newStmt("n1", "a.ts", range(1, 0, 1, 20), "foo()")]),
    []
  );
  eq("identical range: one inherited", report.inherited.length, 1);
  eq("identical range: source id", report.inherited[0].sources[0].oldId, "s1");
  eq("identical range: hits kept", report.inherited[0].sources[0].hits, 5);
  eq("identical range: provenance", report.inherited[0].provenance, [
    "auto:identical-range",
  ]);
  eq("identical range: nothing uncertain", report.uncertain.length, 0);
}

// 2. Explicit move rule: statement moved to a different location.
{
  const rules: MappingRule[] = [
    { kind: "move", from: "s1", to: "n9", reason: "extracted into helper" },
  ];
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(3, 0, 3, 10), 7, "x += 1")]),
    newData([newStmt("n9", "b.ts", range(40, 4, 40, 11), "x += 1")]),
    rules
  );
  eq("move: inherited count", report.inherited.length, 1);
  eq("move: target", report.inherited[0].newId, "n9");
  eq("move: hits", report.inherited[0].sources[0].hits, 7);
  eq("move: provenance", report.inherited[0].provenance, ["explicit:move"]);
}

// 3. Split rule: one old statement becomes several new statements.
{
  const rules: MappingRule[] = [{ kind: "split", from: "s1", to: ["n1", "n2"] }];
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(2, 0, 2, 30), 4, "a(); b()")]),
    newData([
      newStmt("n1", "a.ts", range(2, 0, 2, 4), "a()"),
      newStmt("n2", "a.ts", range(3, 0, 3, 4), "b()"),
    ]),
    rules
  );
  eq("split: both targets inherit", report.inherited.length, 2);
  eq("split: each target keeps the single source", [
    report.inherited[0].sources.length,
    report.inherited[1].sources.length,
  ], [1, 1]);
  eq("split: source hits", report.inherited[0].sources[0].hits, 4);
}

// 4. Merge rule: many old statements to one new statement, sources kept separately.
{
  const rules: MappingRule[] = [{ kind: "merge", from: ["s1", "s2"], to: "n1" }];
  const report = migrateCoverage(
    oldData([
      oldStmt("s1", "a.ts", range(2, 0, 2, 4), 3, "a()"),
      oldStmt("s2", "a.ts", range(3, 0, 3, 4), 12, "b()"),
    ]),
    newData([newStmt("n1", "a.ts", range(2, 0, 2, 9), "a(); b()")]),
    rules
  );
  eq("merge: single inherited entry", report.inherited.length, 1);
  eq("merge: two sources kept separately", report.inherited[0].sources.length, 2);
  eq("merge: per-source hits preserved", [
    report.inherited[0].sources[0].hits,
    report.inherited[0].sources[1].hits,
  ], [3, 12]);
  eq("merge: source ids", [
    report.inherited[0].sources[0].oldId,
    report.inherited[0].sources[1].oldId,
  ], ["s1", "s2"]);
}

// 5. Content hash auto-match: statement moved, text proves correspondence.
{
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(5, 0, 5, 15), 9, "return  x + 1")]),
    newData([newStmt("n1", "a.ts", range(50, 2, 50, 17), "return x   + 1")]),
    []
  );
  eq("content hash: inherited", report.inherited.length, 1);
  eq("content hash: provenance", report.inherited[0].provenance, [
    "auto:content-hash",
  ]);
  eq("content hash: hits", report.inherited[0].sources[0].hits, 9);
}

// 6. Ambiguous content: two identical new statements, no unique proof.
{
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(5, 0, 5, 8), 9, "tick()")]),
    newData([
      newStmt("n1", "a.ts", range(10, 0, 10, 8), "tick()"),
      newStmt("n2", "a.ts", range(20, 0, 20, 8), "tick()"),
    ]),
    []
  );
  eq("ambiguous: nothing inherited", report.inherited.length, 0);
  eq("ambiguous: old is uncertain", report.uncertain.length, 1);
  eq("ambiguous: candidates listed", report.uncertain[0].candidates, ["n1", "n2"]);
  eq("ambiguous: both new are added", report.added.length, 2);
}

// 7. No line-number shifting: same file, shifted range, different text, no rule.
{
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(10, 0, 10, 12), 6, "oldCall()")]),
    newData([newStmt("n1", "a.ts", range(20, 0, 20, 12), "otherCall()")]),
    []
  );
  eq("no line shifting: nothing inherited", report.inherited.length, 0);
  eq("no line shifting: old uncertain", report.uncertain.length, 1);
  eq("no line shifting: new added", report.added.length, 1);
}

// 8. Conflicting explicit rules: statement claimed twice becomes uncertain.
{
  const rules: MappingRule[] = [
    { kind: "move", from: "s1", to: "n1" },
    { kind: "move", from: "s1", to: "n2" },
  ];
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(1, 0, 1, 5), 2, "go()")]),
    newData([
      newStmt("n1", "a.ts", range(8, 0, 8, 5), "go()"),
      newStmt("n2", "a.ts", range(9, 0, 9, 5), "go()"),
    ]),
    rules
  );
  eq("conflict: nothing inherited", report.inherited.length, 0);
  eq("conflict: old uncertain", report.uncertain.length, 1);
  eq("conflict: diagnostics emitted", report.diagnostics.length > 0, true);
}

// 9. Dangling rule reference produces a diagnostic, not a crash.
{
  const rules: MappingRule[] = [{ kind: "move", from: "ghost", to: "n1" }];
  const report = migrateCoverage(
    oldData([oldStmt("s1", "a.ts", range(1, 0, 1, 5), 2, "go()")]),
    newData([newStmt("n1", "a.ts", range(1, 0, 1, 5), "go()")]),
    rules
  );
  eq("dangling rule: diagnostic", report.diagnostics.length, 1);
  eq("dangling rule: identical range still matches", report.inherited.length, 1);
}

// 10. Mixed scenario: inherited + added + uncertain in one report.
{
  const rules: MappingRule[] = [{ kind: "merge", from: ["s2", "s3"], to: "n2" }];
  const report = migrateCoverage(
    oldData([
      oldStmt("s1", "a.ts", range(1, 0, 1, 6), 1, "keep()"),
      oldStmt("s2", "a.ts", range(2, 0, 2, 4), 3, "a()"),
      oldStmt("s3", "a.ts", range(3, 0, 3, 4), 4, "b()"),
      oldStmt("s4", "a.ts", range(4, 0, 4, 9), 8, "removed()"),
    ]),
    newData([
      newStmt("n1", "a.ts", range(1, 0, 1, 6), "keep()"),
      newStmt("n2", "a.ts", range(2, 0, 2, 9), "a(); b()"),
      newStmt("n3", "a.ts", range(3, 0, 3, 7), "brand()"),
    ]),
    rules
  );
  eq("mixed: summary", report.summary, {
    oldTotal: 4,
    newTotal: 3,
    inherited: 2,
    added: 1,
    uncertain: 1,
  });
  eq("mixed: uncertain is the removed statement", report.uncertain[0].oldId, "s4");
  eq("mixed: added is the brand new statement", report.added[0].newId, "n3");
}

console.log("");
console.log(checks + " checks, " + failures + " failures");
if (failures > 0) {
  process.exitCode = 1;
}
