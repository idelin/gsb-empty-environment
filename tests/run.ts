/* Dependency-free test runner. */

import { readFileSync } from "fs";
import { analyze } from "../src/analyzer";
import { parseCondition, refine, collectDomains, killVariable } from "../src/conditions";
import { Graph, parseGraph } from "../src/model";

let passed = 0;
let failed = 0;

function check(name: string, cond: boolean, detail?: string): void {
  if (cond) {
    passed++;
    process.stdout.write(`ok   ${name}\n`);
  } else {
    failed++;
    process.stdout.write(`FAIL ${name}${detail ? ` -- ${detail}` : ""}\n`);
  }
}

function loadExample(path: string): Graph {
  return parseGraph(JSON.parse(readFileSync(path, "utf8")));
}

function nodeIds(g: Graph, w: ReturnType<typeof analyze>): string[] {
  return w.path.map((s) => s.nodeId);
}

// ---------- condition unit tests ----------
{
  const clauses = parseCondition("flag == true && step != 2");
  check("cond: parses conjunction", clauses.length === 2);
  check("cond: clause fields", clauses[0]?.variable === "flag" && clauses[0]?.op === "==");

  const domains = collectDomains(["flag == true", "flag == false"]);
  const s1 = refine(new Map(), domains, "flag == true");
  check("cond: refine keeps state", s1 !== null);
  const s2 = refine(s1 as Map<string, Set<string>>, domains, "flag == false");
  check("cond: contradiction is infeasible", s2 === null);

  const s3 = refine(new Map(), domains, "flag != true");
  const s4 = refine(s3 as Map<string, Set<string>>, domains, "flag == false");
  check("cond: !=true then ==false feasible", s4 !== null);
  const s5 = refine(s3 as Map<string, Set<string>>, domains, "flag == true");
  check("cond: !=true then ==true infeasible", s5 === null);

  const killed = killVariable(s3 as Map<string, Set<string>>, "flag");
  const s6 = refine(killed, domains, "flag == true");
  check("cond: kill restores full domain", s6 !== null);

  let threw = false;
  try {
    parseCondition("flag === true");
  } catch {
    threw = true;
  }
  check("cond: rejects invalid syntax", threw);
}

// ---------- example 1: basic ----------
{
  const g = loadExample("examples/01-basic.json");
  const w = analyze(g);
  check("ex1: dangerous path found", w.found);
  check(
    "ex1: path is n0->n1->n2",
    JSON.stringify(nodeIds(g, w)) === JSON.stringify(["n0", "n1", "n2"])
  );
  check(
    "ex1: taint source reported",
    w.dangers.length === 1 && w.dangers[0]?.arg === "q" && w.dangers[0]?.sources.join(",") === "user_input"
  );
}

// ---------- example 2: sanitizer type mismatch ----------
{
  const g = loadExample("examples/02-sanitizer-types.json");
  const w = analyze(g);
  check("ex2: dangerous path found", w.found);
  check(
    "ex2: goes through the ineffective sanitizer",
    JSON.stringify(nodeIds(g, w)) === JSON.stringify(["n0", "n1", "n2", "n4"]),
    JSON.stringify(nodeIds(g, w))
  );
  const notes = w.path.flatMap((s) => s.notes).join("\n");
  check("ex2: ineffectiveness explained", notes.includes("ineffective"), notes);
}

// ---------- example 3: alias invalidation ----------
{
  const g = loadExample("examples/03-alias-invalidation.json");
  const w = analyze(g);
  check("ex3: alias write re-taints sanitized var", w.found);
  const notes = w.path.flatMap((s) => s.notes).join("\n");
  check("ex3: invalidation noted", notes.includes("invalidated sanitization of q"), notes);
}

// ---------- example 4: contradictory branches ----------
{
  const g = loadExample("examples/04-contradictory-branches.json");
  const w = analyze(g);
  check("ex4: feasible dangerous path found", w.found);
  check(
    "ex4: contradictory branch excluded",
    JSON.stringify(nodeIds(g, w)) === JSON.stringify(["n0", "n1", "n4", "n5"]),
    JSON.stringify(nodeIds(g, w))
  );
}

// ---------- example 5: safe ----------
{
  const g = loadExample("examples/05-safe.json");
  const w = analyze(g);
  check("ex5: no dangerous path", !w.found);
}

// ---------- inline: may-alias weak update ----------
{
  const g = parseGraph({
    entry: "n0",
    rules: {
      sources: [{ id: "user_input", type: "string" }],
      sanitizers: [{ id: "escape_str", types: ["string"] }],
      sinks: [{ id: "exec", dangerTypes: ["string"] }],
    },
    nodes: [
      { id: "n0", op: "source", source: "user_input", var: "u" },
      { id: "n1", op: "assign", var: "a", expr: ["u"] },
      { id: "n2", op: "sanitize", sanitizer: "escape_str", var: "a" },
      { id: "n3", op: "alias", var: "p", pointsToAny: ["a", "b"] },
      { id: "n4", op: "store", pointer: "p", expr: ["u"] },
      { id: "n5", op: "sink", sink: "exec", args: ["a"] },
    ],
    edges: [
      { from: "n0", to: "n1" },
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
      { from: "n3", to: "n4" },
      { from: "n4", to: "n5" },
    ],
  });
  const w = analyze(g);
  check("may-alias: weak update re-taints sanitized target", w.found);
}

// ---------- inline: may-alias weak update with clean expr keeps taint ----------
{
  const g = parseGraph({
    entry: "n0",
    rules: {
      sources: [{ id: "user_input", type: "string" }],
      sanitizers: [],
      sinks: [{ id: "exec" }],
    },
    nodes: [
      { id: "n0", op: "source", source: "user_input", var: "u" },
      { id: "n1", op: "assign", var: "a", expr: ["u"] },
      { id: "n2", op: "alias", var: "p", pointsToAny: ["a", "b"] },
      { id: "n3", op: "store", pointer: "p", expr: ["c"] },
      { id: "n4", op: "sink", sink: "exec", args: ["a"] },
    ],
    edges: [
      { from: "n0", to: "n1" },
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
      { from: "n3", to: "n4" },
    ],
  });
  const w = analyze(g);
  check("may-alias: weak update with clean rhs keeps old taint", w.found);
}

// ---------- inline: strong update with clean expr cleans ----------
{
  const g = parseGraph({
    entry: "n0",
    rules: {
      sources: [{ id: "user_input", type: "string" }],
      sanitizers: [],
      sinks: [{ id: "exec" }],
    },
    nodes: [
      { id: "n0", op: "source", source: "user_input", var: "u" },
      { id: "n1", op: "assign", var: "a", expr: ["u"] },
      { id: "n2", op: "alias", var: "p", pointsTo: "a" },
      { id: "n3", op: "store", pointer: "p", expr: ["c"] },
      { id: "n4", op: "sink", sink: "exec", args: ["a"] },
    ],
    edges: [
      { from: "n0", to: "n1" },
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
      { from: "n3", to: "n4" },
    ],
  });
  const w = analyze(g);
  check("alias: strong update with clean rhs cleans target", !w.found);
}

// ---------- inline: load through alias ----------
{
  const g = parseGraph({
    entry: "n0",
    rules: {
      sources: [{ id: "user_input", type: "string" }],
      sanitizers: [],
      sinks: [{ id: "exec" }],
    },
    nodes: [
      { id: "n0", op: "source", source: "user_input", var: "u" },
      { id: "n1", op: "alias", var: "p", pointsTo: "u" },
      { id: "n2", op: "load", var: "r", pointer: "p" },
      { id: "n3", op: "sink", sink: "exec", args: ["r"] },
    ],
    edges: [
      { from: "n0", to: "n1" },
      { from: "n1", to: "n2" },
      { from: "n2", to: "n3" },
    ],
  });
  const w = analyze(g);
  check("load: taint propagates through alias", w.found && w.dangers[0]?.arg === "r");
}

// ---------- inline: assignment kills condition knowledge ----------
{
  const g = parseGraph({
    entry: "n0",
    rules: {
      sources: [{ id: "user_input", type: "string" }],
      sanitizers: [],
      sinks: [{ id: "exec" }],
    },
    nodes: [
      { id: "n0", op: "source", source: "user_input", var: "u" },
      { id: "n1", op: "assign", var: "flag", expr: ["c"] },
      { id: "n2", op: "sink", sink: "exec", args: ["u"] },
    ],
    edges: [
      { from: "n0", to: "n1", cond: "flag == true" },
      { from: "n1", to: "n2", cond: "flag == false" },
    ],
  });
  const w = analyze(g);
  check("cond: reassignment makes contradictory guard feasible", w.found);
}

// ---------- inline: sink dangerTypes filter ----------
{
  const g = parseGraph({
    entry: "n0",
    rules: {
      sources: [{ id: "user_input", type: "string" }],
      sanitizers: [],
      sinks: [{ id: "query", dangerTypes: ["sql"] }],
    },
    nodes: [
      { id: "n0", op: "source", source: "user_input", var: "u" },
      { id: "n1", op: "sink", sink: "query", args: ["u"] },
    ],
    edges: [{ from: "n0", to: "n1" }],
  });
  const w = analyze(g);
  check("sink: type not in dangerTypes is safe", !w.found);
}

// ---------- inline: loop terminates ----------
{
  const g = parseGraph({
    entry: "n0",
    rules: {
      sources: [{ id: "user_input", type: "string" }],
      sanitizers: [],
      sinks: [{ id: "exec" }],
    },
    nodes: [
      { id: "n0", op: "source", source: "user_input", var: "u" },
      { id: "n1", op: "assign", var: "u", expr: ["u"] },
      { id: "n2", op: "sink", sink: "exec", args: ["u"] },
    ],
    edges: [
      { from: "n0", to: "n1" },
      { from: "n1", to: "n1" },
      { from: "n1", to: "n2" },
    ],
  });
  const w = analyze(g);
  check("loop: terminates and finds sink", w.found);
}

// ---------- inline: invalid graph rejected ----------
{
  let threw = false;
  try {
    parseGraph({ entry: "x", rules: { sources: [], sanitizers: [], sinks: [] }, nodes: [], edges: [] });
  } catch {
    threw = true;
  }
  check("model: empty nodes rejected", threw);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
