import test from "node:test";
import assert from "node:assert/strict";
import {
  admissionWorstCase,
  assessAdmission,
  verifyCertificate,
} from "../src/admission.ts";

// Exact fraction helpers for the independent brute-force oracle.
function gcd(a, b) {
  a = a < 0n ? -a : a; b = b < 0n ? -b : b;
  while (b) { [a, b] = [b, a % b]; }
  return a;
}
function fr(n, d = 1n) {
  if (d < 0n) { n = -n; d = -d; }
  if (n === 0n) return [0n, 1n];
  const g = gcd(n, d);
  return [n / g, d / g];
}
const add = (a, b) => fr(a[0] * b[1] + b[0] * a[1], a[1] * b[1]);
const sub = (a, b) => fr(a[0] * b[1] - b[0] * a[1], a[1] * b[1]);
const mul = (a, b) => fr(a[0] * b[0], a[1] * b[1]);
const max0 = (a) => (a[0] < 0n ? [0n, 1n] : a);
const ge = (a, b) => a[0] * b[1] >= b[0] * a[1];
const parse = (s) => { const [n, d] = s.split("/"); return fr(BigInt(n), BigInt(d)); };

// Deterministic (answer, nextState) transition helper.
function det(answer, nextState) {
  return [{ answer, nextState, probability: "1/1" }];
}

// Brute-force enumeration of every history-based adaptive policy. The same
// public history selects the same query on both datasets, as required.
function bruteForce(spec, L) {
  const rounds = spec.rounds;
  const answers = spec.answers;
  const lmul = parse(L);
  const stopSet = new Set(
    spec.stopHistories.map((h) => h.map(([q, a]) => q + ">" + a).join("|")),
  );

  function row(side, query, state) {
    const entries = spec.tables[side][query][state];
    const merged = new Map();
    for (const e of entries) {
      const cur = merged.get(e.answer) ?? { total: [0n, 1n], next: new Map() };
      const p = parse(e.probability);
      cur.total = add(cur.total, p);
      cur.next.set(e.nextState, add(cur.next.get(e.nextState) ?? [0n, 1n], p));
      merged.set(e.answer, cur);
    }
    return merged;
  }

  function mass(dist) {
    let total = [0n, 1n];
    for (const v of dist.values()) total = add(total, v);
    return total;
  }

  function step(dist, side, query, answer) {
    const next = new Map();
    for (const [state, stateMass] of dist) {
      const branch = row(side, query, state).get(answer);
      if (!branch) continue;
      for (const [ns, joint] of branch.next) {
        next.set(ns, add(next.get(ns) ?? [0n, 1n], mul(stateMass, joint)));
      }
    }
    return next;
  }

  function diverge(transcripts) {
    let d01 = [0n, 1n];
    let d10 = [0n, 1n];
    for (const [p0, p1] of transcripts.values()) {
      d01 = add(d01, max0(sub(p0, mul(lmul, p1))));
      d10 = add(d10, max0(sub(p1, mul(lmul, p0))));
    }
    return [d01, d10];
  }

  function recurse(round, historyKey, dist0, dist1) {
    const stopped = round >= rounds || stopSet.has(historyKey);
    if (stopped) {
      const key = historyKey === "" ? "ROOT_STOP" : historyKey;
      return new Map([[key, [mass(dist0), mass(dist1)]]]);
    }
    let bestTranscripts = null;
    let bestScore = null;
    for (const query of spec.allowedQueries[round]) {
      const transcripts = new Map();
      for (const answer of answers) {
        const next0 = step(dist0, 0, query, answer);
        const next1 = step(dist1, 1, query, answer);
        const m0 = mass(next0);
        const m1 = mass(next1);
        if (m0[0] === 0n && m1[0] === 0n) continue;
        const suffixKey = historyKey === ""
          ? query + ">" + answer
          : historyKey + "|" + query + ">" + answer;
        for (const [tail, probs] of recurse(round + 1, suffixKey, next0, next1)) {
          if (transcripts.has(tail)) throw new Error("duplicate transcript");
          transcripts.set(tail, probs);
        }
      }
      const score = diverge(transcripts);
      if (
        bestScore === null ||
        ge(score[0], bestScore[0]) || ge(score[1], bestScore[1])
      ) {
        bestScore = score;
        bestTranscripts = transcripts;
      }
    }
    return bestTranscripts;
  }

  const initial = new Map();
  spec.states.forEach((s, i) => initial.set(s, parse(spec.initial[i])));
  const transcripts = recurse(0, "", initial, new Map(initial));
  const [d01, d10] = diverge(transcripts);
  return { d01, d10 };
}

function smallSpec() {
  const states = ["s0", "s1"];
  const answers = ["a", "b"];
  const queries = ["q0", "q1"];
  const tables = [
    {
      q0: {
        s0: det("a", "s1"),
        s1: det("b", "s0"),
      },
      q1: {
        s0: [
          { answer: "a", nextState: "s0", probability: "1/2" },
          { answer: "b", nextState: "s1", probability: "1/2" },
        ],
        s1: det("a", "s0"),
      },
    },
    {
      q0: {
        s0: det("b", "s1"),
        s1: det("a", "s0"),
      },
      q1: {
        s0: det("b", "s1"),
        s1: [
          { answer: "a", nextState: "s1", probability: "1/3" },
          { answer: "b", nextState: "s0", probability: "2/3" },
        ],
      },
    },
  ];
  return {
    rounds: 2,
    states,
    answers,
    queries,
    allowedQueries: [["q0", "q1"], ["q0", "q1"]],
    initial: ["1/1", "0/1"],
    multiplier: "1/1",
    stopHistories: [],
    tables,
  };
}

test("small table matches brute-force adaptive enumeration", () => {
  const spec = smallSpec();
  const result = admissionWorstCase(spec);
  const oracle = bruteForce(spec, spec.multiplier);
  assert.equal(result.direction01, oracle.d01.join("/"));
  assert.equal(result.direction10, oracle.d10.join("/"));
  const worst = ge(oracle.d01, oracle.d10) ? oracle.d01 : oracle.d10;
  assert.equal(result.worstDivergence, worst.join("/"));
  const report = verifyCertificate(spec, result.certificate);
  assert.equal(report.valid, true, report.errors.join("; "));
});

test("three rounds with merged answers and zero-side branches", () => {
  const spec = smallSpec();
  spec.rounds = 3;
  spec.allowedQueries = [["q0", "q1"], ["q0", "q1"], ["q0"]];
  // Two hidden successor states share answer "a" on side 0; side 1 never
  // emits "a" from s1 under q1, creating a one-sided zero branch.
  spec.tables[0].q1.s0 = [
    { answer: "a", nextState: "s0", probability: "1/2" },
    { answer: "a", nextState: "s1", probability: "1/4" },
    { answer: "b", nextState: "s1", probability: "1/4" },
  ];
  spec.tables[1].q1.s1 = [{ answer: "b", nextState: "s0", probability: "1/1" }];
  const result = admissionWorstCase(spec);
  const oracle = bruteForce(spec, spec.multiplier);
  assert.equal(result.direction01, oracle.d01.join("/"));
  assert.equal(result.direction10, oracle.d10.join("/"));
  assert.equal(verifyCertificate(spec, result.certificate).valid, true);
});

test("stopping is a visible transcript event", () => {
  const spec = smallSpec();
  spec.stopHistories = [[["q0", "a"]]];
  const result = admissionWorstCase(spec);
  const oracle = bruteForce(spec, spec.multiplier);
  assert.equal(result.direction01, oracle.d01.join("/"));
  assert.equal(result.direction10, oracle.d10.join("/"));
  assert.equal(verifyCertificate(spec, result.certificate).valid, true);
});

test("multiplier above one and admission budget", () => {
  const spec = smallSpec();
  spec.multiplier = "3/2";
  const result = admissionWorstCase(spec);
  const oracle = bruteForce(spec, spec.multiplier);
  assert.equal(result.direction01, oracle.d01.join("/"));
  assert.equal(result.direction10, oracle.d10.join("/"));
  spec.delta = result.worstDivergence;
  assert.equal(assessAdmission(spec).admitted, true);
  spec.delta = "0/1";
  assert.equal(assessAdmission(spec).admitted, false);
});

test("certificate tampering is rejected", () => {
  const spec = smallSpec();
  const result = admissionWorstCase(spec);
  const tampered = structuredClone(result.certificate);
  tampered.nodes[tampered.rootKey].value01 = "1/7";
  assert.equal(verifyCertificate(spec, tampered).valid, false);

  const tamperedEdge = structuredClone(result.certificate);
  const anyKey = Object.keys(tamperedEdge.nodes).find(
    (k) => tamperedEdge.nodes[k].branches,
  );
  const query = Object.keys(tamperedEdge.nodes[anyKey].branches)[0];
  tamperedEdge.nodes[anyKey].branches[query][0].prob0 = "1/2";
  assert.equal(verifyCertificate(spec, tamperedEdge).valid, false);
});

test("repeated posterior states collapse in an eight-round shared instance", () => {
  // Single hidden state: every round returns to the same posterior, so the
  // reachable DP graph has only O(rounds) distinct states despite 2^8
  // adaptive histories.
  const spec = {
    rounds: 8,
    states: ["s"],
    answers: ["a", "b"],
    queries: ["q0", "q1"],
    allowedQueries: Array.from({ length: 8 }, () => ["q0", "q1"]),
    initial: ["1/1"],
    multiplier: "1/1",
    stopHistories: [],
    tables: [
      {
        q0: {
          s: [
            { answer: "a", nextState: "s", probability: "3/4" },
            { answer: "b", nextState: "s", probability: "1/4" },
          ],
        },
        q1: { s: det("a", "s") },
      },
      {
        q0: {
          s: [
            { answer: "a", nextState: "s", probability: "1/2" },
            { answer: "b", nextState: "s", probability: "1/2" },
          ],
        },
        q1: { s: det("a", "s") },
      },
    ],
  };
  const result = admissionWorstCase(spec);
  assert.equal(result.distinctStates, 8); // one shared state per non-terminal round
  assert.equal(result.direction01, "1/4");
  assert.equal(result.direction10, "1/4");
  assert.equal(result.worstDivergence, "1/4");
  assert.equal(verifyCertificate(spec, result.certificate).valid, true);

  // Independent check via exhaustive recursion over public histories with
  // memoization on remaining rounds (single-state beliefs make the value
  // depend only on the round). This mirrors the Bellman optimum without
  // importing production internals.
  const a0 = parse("3/4");
  const a1 = parse("1/2");
  const b0 = parse("1/4");
  const b1 = parse("1/2");
  const historyDp = (remaining) => {
    if (remaining === 0) return { v01: [0n, 1n], v10: [0n, 1n] };
    const child = historyDp(remaining - 1);
    const last = remaining === 1;
    // Without a stop rule every answer continues; only the round cap ends a
    // transcript, at which point each answer contributes its own term.
    let q0_01;
    let q0_10;
    if (last) {
      q0_01 = add(max0(sub(a0, a1)), max0(sub(b0, b1)));
      q0_10 = add(max0(sub(a1, a0)), max0(sub(b1, b0)));
    } else {
      q0_01 = mul(add(a0, b0), child.v01);
      q0_10 = mul(add(a1, b1), child.v10);
    }
    // q1 emits only "a", identical on both sides, so its terminal term is 0.
    const q1_01 = last ? [0n, 1n] : child.v01;
    const q1_10 = last ? [0n, 1n] : child.v10;
    return {
      v01: ge(q0_01, q1_01) ? q0_01 : q1_01,
      v10: ge(q0_10, q1_10) ? q0_10 : q1_10,
    };
  };
  const oracle8 = historyDp(8);
  assert.equal(result.direction01, oracle8.v01.join("/"));
  assert.equal(result.direction10, oracle8.v10.join("/"));
});

test("eight rounds with shared hidden-state randomness reuse posteriors", () => {
  // Two hidden states where every answer resets to the same uniform posterior
  // pair on both datasets, so histories converge onto shared memo nodes.
  const half = [
    { answer: "a", nextState: "s0", probability: "1/2" },
    { answer: "b", nextState: "s1", probability: "1/2" },
  ];
  const thirds = [
    { answer: "a", nextState: "s0", probability: "1/3" },
    { answer: "b", nextState: "s1", probability: "2/3" },
  ];
  const spec = {
    rounds: 8,
    states: ["s0", "s1"],
    answers: ["a", "b"],
    queries: ["q"],
    allowedQueries: Array.from({ length: 8 }, () => ["q"]),
    initial: ["1/1", "0/1"],
    multiplier: "1/1",
    stopHistories: [],
    tables: [
      { q: { s0: half, s1: half } },
      { q: { s0: thirds, s1: thirds } },
    ],
  };
  const result = admissionWorstCase(spec);
  // Far fewer memo nodes than the 255 public histories of a full tree.
  assert.ok(result.distinctStates < 40, `got ${result.distinctStates}`);
  assert.equal(result.worstDivergence, "1/6");
  assert.equal(verifyCertificate(spec, result.certificate).valid, true);

  // Independent expectation: after round one both sides are uniform, hence
  // indistinguishable from round two onward; only round one (3/4 vs 2/3...
  // here 1/2 vs 1/3 on answer "a") contributes.
  const expected = add(max0(sub(parse("1/2"), parse("1/3"))),
    max0(sub(parse("1/2"), parse("2/3"))));
  assert.equal(result.direction01, expected.join("/"));
});

test("immediate stop rule emits only the empty transcript", () => {
  const spec = smallSpec();
  spec.stopHistories = [[]];
  const result = admissionWorstCase(spec);
  assert.equal(result.distinctStates, 1);
  assert.equal(result.worstDivergence, "0/1");
  assert.equal(verifyCertificate(spec, result.certificate).valid, true);
});

test("invalid specs are rejected", () => {
  const spec = smallSpec();
  assert.throws(() => admissionWorstCase({ ...spec, rounds: 9 }));
  assert.throws(() => admissionWorstCase({ ...spec, multiplier: "1/2" }));
  const bad = smallSpec();
  bad.tables[0].q0.s0 = [{ answer: "a", nextState: "s1", probability: "1/2" }];
  assert.throws(() => admissionWorstCase(bad));
});
