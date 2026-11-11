import { test } from "node:test";
import assert from "node:assert/strict";
import {
  analyze,
  ap,
  checkNoFalseNegatives,
  referenceFindings,
  type AnalysisResult,
  type Program,
  type Stmt,
} from "../index.ts";

const pairSet = (r: AnalysisResult): Set<string> =>
  new Set(
    r.findings.flatMap((f) => f.sources.map((s) => `${s.sourceId}->${f.sinkFn}:${f.sinkId}`)),
  );

const wrapperProgram: Program = {
  entry: "main",
  functions: [
    {
      name: "main",
      params: [],
      body: [
        { kind: "source", id: "user-input", target: ap("req") },
        { kind: "call", site: "d1", callee: "dispatch", args: [ap("req")], results: ["t1"] },
        { kind: "call", site: "d2", callee: "dispatch", args: [ap("cfg")], results: ["t2"] },
        { kind: "sink", id: "eval-request", args: [ap("t1")] },
        { kind: "sink", id: "eval-config", args: [ap("t2")] },
      ],
    },
    {
      name: "dispatch",
      params: ["x"],
      body: [
        { kind: "call", site: "w1", callee: "wrap", args: [ap("x")], results: ["y"] },
        { kind: "return", values: [ap("y")] },
      ],
    },
    {
      name: "wrap",
      params: ["v"],
      body: [{ kind: "return", values: [ap("v")] }],
    },
  ],
};

test("k=2 separates trusted config from request data through shared wrappers", () => {
  const result = analyze(wrapperProgram);
  const pairs = pairSet(result);
  assert.ok(pairs.has("user-input->main:eval-request"));
  assert.ok(!pairs.has("user-input->main:eval-config"));
});

test("shallower contexts merge summaries and produce false positives", () => {
  for (const contextDepth of [0, 1]) {
    const pairs = pairSet(analyze(wrapperProgram, { contextDepth }));
    assert.ok(pairs.has("user-input->main:eval-request"));
    assert.ok(pairs.has("user-input->main:eval-config"), `depth ${contextDepth} should merge`);
  }
});

test("findings carry witness context paths for source and sink", () => {
  const result = analyze(wrapperProgram);
  const finding = result.findings.find((f) => f.sinkId === "eval-request")!;
  assert.deepEqual(
    finding.sinkPath.map((s) => `${s.fn}${s.via ? `@${s.via}` : ""}`),
    ["main"],
  );
  const src = finding.sources[0]!;
  assert.equal(src.sourceId, "user-input");
  assert.equal(src.fn, "main");
});

const deepChainProgram: Program = {
  entry: "main",
  functions: [
    {
      name: "main",
      params: [],
      body: [
        { kind: "source", id: "deep-src", target: ap("d") },
        { kind: "call", site: "s1", callee: "a", args: [ap("d")], results: ["r"] },
        { kind: "sink", id: "deep-sink", args: [ap("r")] },
      ],
    },
    {
      name: "a",
      params: ["x"],
      body: [
        { kind: "call", site: "s2", callee: "b", args: [ap("x")], results: ["y"] },
        { kind: "return", values: [ap("y")] },
      ],
    },
    {
      name: "b",
      params: ["x"],
      body: [
        { kind: "call", site: "s3", callee: "c", args: [ap("x")], results: ["y"] },
        { kind: "return", values: [ap("y")] },
      ],
    },
    {
      name: "c",
      params: ["x"],
      body: [
        { kind: "call", site: "s4", callee: "leaf", args: [ap("x")], results: ["y"] },
        { kind: "return", values: [ap("y")] },
      ],
    },
    {
      name: "leaf",
      params: ["v"],
      body: [{ kind: "return", values: [ap("v")] }],
    },
  ],
};

test("call chains deeper than the context limit still propagate taint", () => {
  const pairs = pairSet(analyze(deepChainProgram));
  assert.ok(pairs.has("deep-src->main:deep-sink"));
});

const mergedChainProgram: Program = {
  entry: "main",
  functions: [
    {
      name: "main",
      params: [],
      body: [
        { kind: "source", id: "chain-taint", target: ap("tv") },
        { kind: "call", site: "m1", callee: "a", args: [ap("tv")], results: ["r1"] },
        { kind: "call", site: "m2", callee: "a", args: [ap("cv")], results: ["r2"] },
        { kind: "sink", id: "sink-tainted", args: [ap("r1")] },
        { kind: "sink", id: "sink-clean", args: [ap("r2")] },
      ],
    },
    {
      name: "a",
      params: ["x"],
      body: [
        { kind: "call", site: "ab", callee: "b", args: [ap("x")], results: ["y"] },
        { kind: "return", values: [ap("y")] },
      ],
    },
    {
      name: "b",
      params: ["x"],
      body: [
        { kind: "call", site: "bl", callee: "leaf", args: [ap("x")], results: ["y"] },
        { kind: "return", values: [ap("y")] },
      ],
    },
    {
      name: "leaf",
      params: ["v"],
      body: [
        { kind: "sink", id: "leaf-sink", args: [ap("v")] },
        { kind: "return", values: [ap("v")] },
      ],
    },
  ],
};

test("merging beyond depth stays conservative: taint survives collapsed contexts", () => {
  const result = analyze(mergedChainProgram);
  const pairs = pairSet(result);
  assert.ok(pairs.has("chain-taint->leaf:leaf-sink"));
  assert.ok(pairs.has("chain-taint->main:sink-tainted"));
  const leafSummary = result.summaries.find((s) => s.fn === "leaf")!;
  assert.deepEqual(leafSummary.context, ["ab", "bl"]);
  assert.equal(result.summaries.filter((s) => s.fn === "leaf").length, 1);
});

const recursiveProgram: Program = {
  entry: "main",
  functions: [
    {
      name: "main",
      params: [],
      body: [
        { kind: "source", id: "rec-src", target: ap("d") },
        { kind: "call", site: "r0", callee: "rec", args: [ap("d")], results: ["out"] },
        { kind: "sink", id: "rec-sink", args: [ap("out")] },
      ],
    },
    {
      name: "rec",
      params: ["x"],
      body: [
        { kind: "sink", id: "rec-inner", args: [ap("x")] },
        { kind: "call", site: "rr", callee: "rec", args: [ap("x")], results: ["y"] },
        { kind: "return", values: [ap("x")] },
      ],
    },
  ],
};

test("recursive summaries iterate to a stable fixpoint", () => {
  const result = analyze(recursiveProgram);
  const pairs = pairSet(result);
  assert.ok(pairs.has("rec-src->rec:rec-inner"));
  assert.ok(pairs.has("rec-src->main:rec-sink"));
  assert.ok(result.iterations > result.instances, "fixpoint should re-evaluate instances");
});

const fieldProgram: Program = {
  entry: "main",
  functions: [
    {
      name: "main",
      params: [],
      body: [
        { kind: "source", id: "body-src", target: ap("req", "body") },
        { kind: "assign", target: ap("p"), from: ap("req", "body") },
        { kind: "assign", target: ap("q"), from: ap("req", "headers") },
        { kind: "sink", id: "body-sink", args: [ap("p")] },
        { kind: "sink", id: "header-sink", args: [ap("q")] },
        { kind: "assign", target: ap("o", "f"), from: ap("p") },
        { kind: "sink", id: "via-field", args: [ap("o", "f")] },
        { kind: "sink", id: "via-obj", args: [ap("o")] },
        { kind: "sink", id: "other-field", args: [ap("o", "g")] },
        { kind: "source", id: "deep-field", target: ap("v", "a", "b", "c") },
        { kind: "sink", id: "truncated-read", args: [ap("v", "a", "b", "c", "d")] },
        { kind: "sink", id: "truncated-parent", args: [ap("v", "a")] },
        { kind: "sink", id: "untouched-branch", args: [ap("v", "b")] },
      ],
    },
  ],
};

test("field-level transfers are precise within the access-path budget", () => {
  const pairs = pairSet(analyze(fieldProgram));
  assert.ok(pairs.has("body-src->main:body-sink"));
  assert.ok(!pairs.has("body-src->main:header-sink"));
  assert.ok(pairs.has("body-src->main:via-field"));
  assert.ok(pairs.has("body-src->main:via-obj"));
  assert.ok(!pairs.has("body-src->main:other-field"));
});

test("access-path truncation is conservative in both directions", () => {
  const pairs = pairSet(analyze(fieldProgram));
  assert.ok(pairs.has("deep-field->main:truncated-read"));
  assert.ok(pairs.has("deep-field->main:truncated-parent"));
  assert.ok(!pairs.has("deep-field->main:untouched-branch"));
});

const sanitizeProgram: Program = {
  entry: "main",
  functions: [
    {
      name: "main",
      params: [],
      body: [
        { kind: "source", id: "sx", target: ap("x") },
        { kind: "sanitize", target: ap("x") },
        { kind: "sink", id: "s1", args: [ap("x")] },
        { kind: "source", id: "sy", target: ap("y") },
        { kind: "assign", target: ap("z"), from: ap("y") },
        { kind: "sanitize", target: ap("y") },
        { kind: "sink", id: "s2", args: [ap("z")] },
      ],
    },
  ],
};

test("sanitizers kill only the sanitized value", () => {
  const pairs = pairSet(analyze(sanitizeProgram));
  assert.ok(!pairs.has("sx->main:s1"));
  assert.ok(pairs.has("sy->main:s2"));
});

const allPrograms: Array<[string, Program, number]> = [
  ["wrapper", wrapperProgram, 6],
  ["deepChain", deepChainProgram, 6],
  ["mergedChain", mergedChainProgram, 6],
  ["recursive", recursiveProgram, 5],
  ["field", fieldProgram, 4],
  ["sanitize", sanitizeProgram, 4],
];

for (const [name, program, depth] of allPrograms) {
  test(`reference model confirms no false negatives: ${name}`, () => {
    const result = analyze(program);
    const reference = referenceFindings(program, { maxDepth: depth });
    const check = checkNoFalseNegatives(result, reference);
    assert.ok(check.ok, `missing: ${JSON.stringify(check.missing)}`);
  });
}

test("reference model agrees exactly when no context collision occurs", () => {
  for (const [, program] of allPrograms.filter(([n]) => n !== "recursive" && n !== "mergedChain")) {
    const pairs = pairSet(analyze(program));
    const refPairs = new Set(
      referenceFindings(program, { maxDepth: 8 }).map(
        (r) => `${r.sourceId}->${r.sinkFn}:${r.sinkId}`,
      ),
    );
    assert.deepEqual(pairs, refPairs);
  }
});

function genProgram(seed: number): Program {
  let s = seed >>> 0 || 1;
  const rnd = (): number => {
    s = (s * 1103515245 + 12345) & 0x7fffffff;
    return s / 0x7fffffff;
  };
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rnd() * arr.length)]!;
  const fnCount = 2 + Math.floor(rnd() * 3);
  const names = Array.from({ length: fnCount }, (_, i) => `f${i}`);
  const paramCounts = names.map(() => Math.floor(rnd() * 3));
  const functions = names.map((name, fi) => {
    const params = ["p0", "p1"].slice(0, paramCounts[fi]!);
    const locals = ["v0", "v1", "v2"];
    const vars = [...params, ...locals];
    const fieldSets: string[][] = [[], ["a"], ["b"], ["a", "b"], ["b", "a"]];
    const rp = () => ap(pick(vars), ...pick(fieldSets));
    const body: Stmt[] = [];
    const stmtCount = 3 + Math.floor(rnd() * 5);
    let site = 0;
    let src = 0;
    let snk = 0;
    for (let i = 0; i < stmtCount; i++) {
      const kind = rnd();
      if (kind < 0.25) {
        body.push({ kind: "source", id: `s${fi}-${src++}`, target: rp() });
      } else if (kind < 0.5) {
        body.push({ kind: "assign", target: rp(), from: rp() });
      } else if (kind < 0.6) {
        body.push({ kind: "sanitize", target: rp() });
      } else if (kind < 0.8) {
        const calleeIdx = Math.floor(rnd() * fnCount);
        const argc = paramCounts[calleeIdx]!;
        body.push({
          kind: "call",
          site: `c${fi}-${site++}`,
          callee: names[calleeIdx]!,
          args: Array.from({ length: argc }, () => rp()),
          results: [pick(locals)],
        });
      } else {
        body.push({ kind: "sink", id: `k${fi}-${snk++}`, args: [rp()] });
      }
    }
    body.push({ kind: "return", values: [rp()] });
    return { name, params, body };
  });
  return { entry: "f0", functions };
}

test("generated programs: analysis never misses reference findings", () => {
  for (let seed = 1; seed <= 40; seed++) {
    const program = genProgram(seed);
    const result = analyze(program);
    const reference = referenceFindings(program, { maxDepth: 5 });
    const check = checkNoFalseNegatives(result, reference);
    assert.ok(check.ok, `seed ${seed} missing: ${JSON.stringify(check.missing)}`);
  }
});
