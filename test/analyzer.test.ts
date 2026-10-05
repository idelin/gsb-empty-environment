import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { analyze, matchingRuleIndices, type Assignment } from "../src/analyze.ts";
import { compileTable, type CompiledTable, type TableJson } from "../src/model.ts";
import { findAvoiding, minimalWitness } from "../src/solver.ts";

function loadExample(): CompiledTable {
  const raw = JSON.parse(readFileSync(new URL("../examples/table.json", import.meta.url), "utf8"));
  return compileTable(raw as TableJson);
}

function indicesOf(table: CompiledTable, assignment: Assignment): number[] {
  return table.fields.map((fd) => {
    const v = assignment[fd.name];
    return fd.domain.kind === "enum"
      ? fd.domain.values.indexOf(v as string)
      : (v as number) - fd.domain.min;
  });
}

function rowByName(report: ReturnType<typeof analyze>, name: string) {
  const row = report.rows.find((r) => r.rule === name);
  assert.ok(row, `row ${name} missing`);
  return row;
}

test("condition compilation: range, enum set, negation, shorthand", () => {
  const table = compileTable({
    fields: [
      { name: "n", type: "int", min: 0, max: 10 },
      { name: "c", type: "enum", values: ["a", "b", "c"] },
    ],
    rules: [
      { name: "r1", priority: 1, when: { n: { range: [2, 5] }, c: "b" } },
      { name: "r2", priority: 1, when: { n: { notRange: [2, 5] }, c: { notIn: ["a", "b", "c"] } } },
      { name: "r3", priority: 1, when: { n: [1, 3, 5], c: { in: ["a", "c"] } } },
    ],
  } as TableJson);
  assert.deepEqual(table.rules[0].box[0], [[2, 5]]);
  assert.deepEqual(table.rules[0].box[1], [[1, 1]]);
  assert.deepEqual(table.rules[1].box[0], [[0, 1], [6, 10]]);
  assert.deepEqual(table.rules[1].box[1], []); // negation of everything is empty
  assert.deepEqual(table.rules[2].box[0], [[1, 1], [3, 3], [5, 5]]);
  assert.deepEqual(table.rules[2].box[1], [[0, 0], [2, 2]]);
});

test("findAvoiding: finds a gap or proves coverage", () => {
  // domain 0..9 x 0..9; blockers cover everything except x=9,y=9
  const full: [number, number][] = [[0, 9]];
  const blockers: [number, number][][][] = [
    [[[0, 8]], [[0, 9]]],
    [[[9, 9]], [[0, 8]]],
  ];
  assert.deepEqual(findAvoiding([full, full], blockers), [9, 9]);
  assert.equal(findAvoiding([full, full], [...blockers, [[[9, 9]], [[9, 9]]]]), null);
});

test("example table: per-row statuses", () => {
  const table = loadExample();
  const report = analyze(table);
  const status = Object.fromEntries(report.rows.map((r) => [r.rule, r.status]));
  assert.deepEqual(status, {
    vip: "wins",
    "minor-deny": "wins",
    "high-roller": "wins",
    "pro-mid": "wins",
    "pro-us-high": "ties",
    "eu-low": "wins",
    "pro-us": "covered",
    "eu-any": "covered",
    impossible: "vacuous",
    default: "wins",
  });
});

test("example table: minimal witnesses are exact", () => {
  const table = loadExample();
  const report = analyze(table);
  assert.deepEqual(rowByName(report, "vip").witness, {
    age: 0, income: 500000, tier: "enterprise", region: "us",
  });
  assert.deepEqual(rowByName(report, "high-roller").witness, {
    age: 18, income: 200000, tier: "free", region: "us",
  });
  assert.deepEqual(rowByName(report, "pro-mid").witness, {
    age: 18, income: 50000, tier: "pro", region: "us",
  });
  assert.deepEqual(rowByName(report, "default").witness, {
    age: 18, income: 0, tier: "free", region: "us",
  });
});

test("every winning witness really wins", () => {
  const table = loadExample();
  const report = analyze(table);
  for (const row of report.rows) {
    if (row.status !== "wins") continue;
    const hits = matchingRuleIndices(table, indicesOf(table, row.witness!));
    const topPriority = Math.max(...hits.map((i) => table.rules[i].priority));
    const winners = hits.filter((i) => table.rules[i].priority === topPriority);
    assert.deepEqual(winners, [row.index], `${row.rule} should be the unique top-priority hit`);
  }
});

test("covered rows carry a covering explanation", () => {
  const table = loadExample();
  const report = analyze(table);
  const proUs = rowByName(report, "pro-us");
  assert.deepEqual(proUs.cover!.map((s) => s.rule), ["pro-mid"]);
  const euAny = rowByName(report, "eu-any");
  assert.deepEqual(
    new Set(euAny.cover!.map((s) => s.rule)),
    new Set(["eu-low", "high-roller"]),
  );
});

test("ties row reports its same-priority partners", () => {
  const table = loadExample();
  const report = analyze(table);
  const row = rowByName(report, "pro-us-high");
  assert.equal(row.status, "ties");
  assert.deepEqual(new Set(row.tiePartners), new Set(["high-roller", "pro-mid"]));
  // the tie witness is hit by the row itself and at least one partner
  const hits = matchingRuleIndices(table, indicesOf(table, row.witness!)).map(
    (i) => table.rules[i].name,
  );
  assert.ok(hits.includes("pro-us-high"));
  assert.ok(hits.some((n) => row.tiePartners!.includes(n)));
});

test("same-priority conflicts come with a co-hit input", () => {
  const table = loadExample();
  const report = analyze(table);
  assert.equal(report.conflicts.length, 3);
  for (const conflict of report.conflicts) {
    assert.equal(conflict.masked, false);
    const hits = matchingRuleIndices(table, indicesOf(table, conflict.witness)).map(
      (i) => table.rules[i].name,
    );
    assert.ok(hits.includes(conflict.rules[0]), `${conflict.rules[0]} must hit`);
    assert.ok(hits.includes(conflict.rules[1]), `${conflict.rules[1]} must hit`);
  }
  const pairs = report.conflicts.map((c) => c.rules.slice().sort().join("+")).sort();
  assert.deepEqual(pairs, [
    "high-roller+pro-mid",
    "high-roller+pro-us-high",
    "pro-mid+pro-us-high",
  ]);
});

test("masked conflict: co-hit inputs all shadowed by a higher rule", () => {
  const table = compileTable({
    fields: [{ name: "n", type: "int", min: 0, max: 100 }],
    rules: [
      { name: "boss", priority: 100, when: { n: { range: [0, 60] } } },
      { name: "a", priority: 50, when: { n: { range: [0, 50] } } },
      { name: "b", priority: 50, when: { n: { range: [25, 75] } } },
    ],
  } as TableJson);
  const report = analyze(table);
  assert.equal(report.conflicts.length, 1);
  const c = report.conflicts[0];
  assert.equal(c.masked, true);
  assert.deepEqual(c.witness, { n: 25 });
});

test("large finite domains are not enumerated", () => {
  const table = compileTable({
    fields: [{ name: "n", type: "int", min: 0, max: 5000000 }],
    rules: [
      { name: "lo", priority: 10, when: { n: { range: [0, 2999999] } } },
      { name: "hi", priority: 10, when: { n: { range: [3000000, 4499999] } } },
      { name: "mid", priority: 5, when: { n: { range: [1000000, 4000000] } } },
      { name: "tail", priority: 1, when: { n: { range: [4500000, 5000000] } } },
    ],
  } as TableJson);
  const start = performance.now();
  const report = analyze(table);
  const elapsed = performance.now() - start;
  assert.ok(elapsed < 1000, `analysis took ${elapsed}ms`);
  assert.equal(rowByName(report, "mid").status, "covered");
  assert.deepEqual(
    new Set(rowByName(report, "mid").cover!.map((s) => s.rule)),
    new Set(["lo", "hi"]),
  );
  // tail only wins at the very top of the domain: minimal witness is exact
  assert.deepEqual(rowByName(report, "tail").witness, { n: 4500000 });
});

test("minimalWitness picks the lexicographically smallest input", () => {
  // allowed: x in 0..4, y in 0..4; blocker forces x>=2 or y>=3
  const allowed: [number, number][][] = [[[0, 4]], [[0, 4]]];
  const blocker: [number, number][][] = [[[0, 1]], [[0, 2]]];
  assert.deepEqual(minimalWitness(allowed, [blocker]), [0, 3]);
});

test("validation errors are descriptive", () => {
  assert.throws(
    () =>
      compileTable({
        fields: [{ name: "c", type: "enum", values: ["a"] }],
        rules: [{ name: "r", priority: 1, when: { c: "zzz" } }],
      } as TableJson),
    /unknown enum value/,
  );
  assert.throws(
    () =>
      compileTable({
        fields: [{ name: "n", type: "int", min: 0, max: 5 }],
        rules: [{ name: "r", priority: 1, when: { nope: 1 } }],
      } as TableJson),
    /unknown field/,
  );
});
