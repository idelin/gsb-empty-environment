import { Graph, type Edge } from "../src/graph.ts";
import { Automaton } from "../src/automaton.ts";
import { regularPathQuery, type PathEvidence } from "../src/query.ts";
import { replayEvidence, verifyQueryResults } from "../src/verify.ts";

let failures = 0;
let count = 0;

function test(name: string, fn: () => void): void {
  count++;
  try {
    fn();
    console.log(`ok   ${name}`);
  } catch (err) {
    failures++;
    console.log(`FAIL ${name}`);
    console.log(`     ${(err as Error).message}`);
  }
}

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(msg);
}

function assertEq<T>(actual: T, expected: T, msg: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(`${msg}: expected ${e}, got ${a}`);
}

const edgeIds = (r: PathEvidence): number[] => r.edges.map((e) => e.id);
const endState = (r: PathEvidence): [string, string] => {
  const last = r.states[r.states.length - 1];
  return [last.node, last.state];
};

const e = (id: number, from: string, to: string, label: string): Edge => ({ id, from, to, label });

// ------------------------------------------------------------------
test("empty path: accepting initial state yields the empty word first", () => {
  const g = new Graph([e(1, "A", "B", "a")]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q0", "q1"],
    transitions: [{ from: "q0", label: "a", to: "q1" }],
  });
  const results = regularPathQuery(g, aut, "A");
  assertEq(results.length, 2, "result count");
  assertEq(results[0].edges.length, 0, "first result is the empty path");
  assertEq(results[0].states, [{ node: "A", state: "q0" }], "empty path evidence");
  assertEq(edgeIds(results[1]), [1], "second result");

  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
  assert(report.emptyPath.expected && report.emptyPath.found, "empty path flagged");
});

test("empty path rejected when initial state is not accepting", () => {
  const g = new Graph([e(1, "A", "B", "a")]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q1"],
    transitions: [{ from: "q0", label: "a", to: "q1" }],
  });
  const results = regularPathQuery(g, aut, "A");
  assertEq(results.length, 1, "result count");
  assertEq(edgeIds(results[0]), [1], "only the length-1 path");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
  assert(!report.emptyPath.expected && !report.emptyPath.found, "no empty path");
});

// ------------------------------------------------------------------
test("order: fewest edges beats smaller edge ids", () => {
  // Direct edge id 9 (label x) vs two-edge path ids [1, 2] (labels y z).
  const g = new Graph([e(9, "A", "B", "x"), e(1, "A", "C", "y"), e(2, "C", "B", "z")]);
  const aut = new Automaton({
    states: ["q0", "qx", "qy", "qz"],
    initial: "q0",
    accepting: ["qx", "qz"],
    transitions: [
      { from: "q0", label: "x", to: "qx" },
      { from: "q0", label: "y", to: "qy" },
      { from: "qy", label: "z", to: "qz" },
    ],
  });
  const results = regularPathQuery(g, aut, "A");
  assertEq(results.map(edgeIds), [[9], [1, 2]], "fewest edges first");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
});

test("order: equal length paths sorted by edge-id sequence", () => {
  // Two 2-edge paths to different end nodes: ids [3, 4] and [5, 1].
  // Lex order: [3,4] < [5,1] even though 1 is the smallest id overall.
  const g = new Graph([
    e(5, "A", "D", "y"),
    e(1, "D", "C", "z"),
    e(3, "A", "C", "y"),
    e(4, "C", "B", "z"),
  ]);
  const aut = new Automaton({
    states: ["q0", "q1", "q2"],
    initial: "q0",
    accepting: ["q2"],
    transitions: [
      { from: "q0", label: "y", to: "q1" },
      { from: "q1", label: "z", to: "q2" },
    ],
  });
  const results = regularPathQuery(g, aut, "A");
  assertEq(results.map(edgeIds), [[3, 4], [5, 1]], "lexicographic edge-id order");
  assertEq(results.map(endState), [["B", "q2"], ["C", "q2"]], "distinct end nodes");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
});

test("two paths to the same product state collapse to the best one", () => {
  // Both [3,4] and [5,1] reach (B, q2); only the lex-smaller survives.
  const g = new Graph([
    e(5, "A", "D", "y"),
    e(1, "D", "B", "z"),
    e(3, "A", "C", "y"),
    e(4, "C", "B", "z"),
  ]);
  const aut = new Automaton({
    states: ["q0", "q1", "q2"],
    initial: "q0",
    accepting: ["q2"],
    transitions: [
      { from: "q0", label: "y", to: "q1" },
      { from: "q1", label: "z", to: "q2" },
    ],
  });
  const results = regularPathQuery(g, aut, "A");
  assertEq(results.map(edgeIds), [[3, 4]], "best path per product state");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
});

// ------------------------------------------------------------------
test("same node, different automaton states are distinct results", () => {
  // NFA: on 'a', q0 goes to both q1 and q2; both accepting.
  const g = new Graph([e(1, "A", "B", "a")]);
  const aut = new Automaton({
    states: ["q0", "q1", "q2"],
    initial: "q0",
    accepting: ["q1", "q2"],
    transitions: [
      { from: "q0", label: "a", to: "q1" },
      { from: "q0", label: "a", to: "q2" },
    ],
  });
  const results = regularPathQuery(g, aut, "A");
  assertEq(results.length, 2, "two product states at node B");
  assertEq(results.map(endState), [["B", "q1"], ["B", "q2"]], "distinct automaton states");
  // Evidence differs only in the automaton component.
  assertEq(results[0].states, [
    { node: "A", state: "q0" },
    { node: "B", state: "q1" },
  ], "evidence 1");
  assertEq(results[1].states, [
    { node: "A", state: "q0" },
    { node: "B", state: "q2" },
  ], "evidence 2");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
  assertEq(report.endNodesWithMultipleAutomatonStates, ["B"], "B reached via two states");
});

// ------------------------------------------------------------------
test("unreachable accepting state yields no results", () => {
  const g = new Graph([e(1, "A", "B", "a"), e(2, "B", "C", "a")]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q1"],
    transitions: [{ from: "q0", label: "z", to: "q1" }], // no 'z' edge exists
  });
  const results = regularPathQuery(g, aut, "A");
  assertEq(results.length, 0, "no results");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
  assert(report.confirmedUnreachable, "independent BFS confirms unreachability");
  assertEq(report.reachableAcceptingStates, 0, "ground truth count");
});

// ------------------------------------------------------------------
test("disabled edges are excluded and can reroute the answer", () => {
  const g = new Graph([
    e(1, "A", "B", "x"),
    e(2, "A", "C", "y"),
    e(3, "C", "B", "z"),
  ]);
  const aut = new Automaton({
    states: ["q0", "qx", "qy", "qz"],
    initial: "q0",
    accepting: ["qx", "qz"],
    transitions: [
      { from: "q0", label: "x", to: "qx" },
      { from: "q0", label: "y", to: "qy" },
      { from: "qy", label: "z", to: "qz" },
    ],
  });
  const all = regularPathQuery(g, aut, "A");
  assertEq(all.map(edgeIds), [[1], [2, 3]], "baseline");

  const disabled = new Set([1]);
  const rerouted = regularPathQuery(g, aut, "A", { disabledEdges: disabled });
  assertEq(rerouted.map(edgeIds), [[2, 3]], "direct edge disabled");

  const report = verifyQueryResults(g, aut, "A", { disabledEdges: disabled }, rerouted);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);

  const nothing = regularPathQuery(g, aut, "A", { disabledEdges: new Set([1, 2]) });
  assertEq(nothing.length, 0, "all routes disabled");
  const report2 = verifyQueryResults(g, aut, "A", { disabledEdges: new Set([1, 2]) }, nothing);
  assert(report2.ok && report2.confirmedUnreachable, "unreachable under disabled set");
});

// ------------------------------------------------------------------
test("cycles in graph and automaton still terminate", () => {
  // Graph: self-loop on A, edge to B. Automaton: parity of 'a' then 'b'.
  const g = new Graph([e(1, "A", "A", "a"), e(2, "A", "B", "b")]);
  const aut = new Automaton({
    states: ["even", "odd", "done"],
    initial: "even",
    accepting: ["done"],
    transitions: [
      { from: "even", label: "a", to: "odd" },
      { from: "odd", label: "a", to: "even" },
      { from: "even", label: "b", to: "done" },
      { from: "odd", label: "b", to: "done" },
    ],
  });
  const results = regularPathQuery(g, aut, "A");
  // Product states reachable: (A,even),(A,odd),(B,done). Accepting: (B,done).
  assertEq(results.map(edgeIds), [[2]], "shortest path to done");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
});

test("automaton cycle accepting along the way emits multiple lengths", () => {
  // (a|b)* a over a looping graph node.
  const g = new Graph([e(1, "A", "A", "a"), e(2, "A", "A", "b")]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q1"],
    transitions: [
      { from: "q0", label: "a", to: "q1" },
      { from: "q0", label: "b", to: "q0" },
      { from: "q1", label: "a", to: "q1" },
      { from: "q1", label: "b", to: "q0" },
    ],
  });
  const results = regularPathQuery(g, aut, "A");
  // Accepting product state (A,q1) is visited once, via the best path:
  // 'a' (edge 1) beats 'b...a'. So exactly one result: [1].
  assertEq(results.map(edgeIds), [[1]], "best path per product state");
  const report = verifyQueryResults(g, aut, "A", {}, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
});

// ------------------------------------------------------------------
test("limit returns the optimal prefix", () => {
  const g = new Graph([
    e(1, "A", "B", "a"),
    e(2, "B", "C", "a"),
    e(3, "C", "D", "a"),
  ]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q1"],
    transitions: [
      { from: "q0", label: "a", to: "q1" },
      { from: "q1", label: "a", to: "q1" },
    ],
  });
  const limited = regularPathQuery(g, aut, "A", { limit: 2 });
  assertEq(limited.map(edgeIds), [[1], [1, 2]], "first two results only");
  const report = verifyQueryResults(g, aut, "A", { limit: 2 }, limited);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
  assertEq(report.reachableAcceptingStates, 3, "three accepting states exist");
});

// ------------------------------------------------------------------
test("target restricts the end node", () => {
  const g = new Graph([e(1, "A", "B", "a"), e(2, "A", "C", "a")]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q1"],
    transitions: [{ from: "q0", label: "a", to: "q1" }],
  });
  const results = regularPathQuery(g, aut, "A", { target: "C" });
  assertEq(results.map(edgeIds), [[2]], "only the path to C");
  const report = verifyQueryResults(g, aut, "A", { target: "C" }, results);
  assert(report.ok, `report errors: ${report.errors.join("; ")}`);
});

// ------------------------------------------------------------------
test("replay evidence detects tampering", () => {
  const g = new Graph([e(1, "A", "B", "a")]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q1"],
    transitions: [{ from: "q0", label: "a", to: "q1" }],
  });
  const [good] = regularPathQuery(g, aut, "A");
  assertEq(replayEvidence(g, aut, "A", good), [], "honest evidence replays clean");

  const wrongState: PathEvidence = {
    edges: good.edges,
    states: [good.states[0], { node: "B", state: "q0" }],
  };
  assert(replayEvidence(g, aut, "A", wrongState).length > 0, "wrong automaton state caught");

  const disabledUse: PathEvidence = good;
  assert(
    replayEvidence(g, aut, "A", disabledUse, new Set([1])).length > 0,
    "disabled edge use caught",
  );

  const notAccepting: PathEvidence = { edges: [], states: [{ node: "A", state: "q0" }] };
  assert(replayEvidence(g, aut, "A", notAccepting).length > 0, "non-accepting end caught");
});

// ------------------------------------------------------------------
test("verifier flags fabricated results", () => {
  const g = new Graph([e(1, "A", "B", "a")]);
  const aut = new Automaton({
    states: ["q0", "q1"],
    initial: "q0",
    accepting: ["q1"],
    transitions: [{ from: "q0", label: "a", to: "q1" }],
  });
  // Claiming zero results when one exists must fail verification.
  const report = verifyQueryResults(g, aut, "A", {}, []);
  assert(!report.ok, "missing result detected");
  assert(report.errors.some((x) => x.includes("missing")), "completeness error reported");
});

console.log(`\n${count - failures}/${count} tests passed`);
if (failures > 0) process.exit(1);
