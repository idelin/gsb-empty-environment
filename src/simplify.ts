/**
 * Sound simplifier for the three-valued policy algebra.
 *
 * Every implemented rule is checked by the exhaustive equivalence tester in
 * tester.ts. Notably:
 *
 *   - x OR NOT x is NEVER rewritten to true when x lives on a nullable domain
 *     (missing x makes the expression MISSING, not TRUE). It only collapses
 *     for required boolean attributes, which this schema language does not
 *     have; therefore the rule is simply absent.
 *   - false AND ... -> false (false branch carries its provenance).
 *   - Constants never erase a result-distinguishing MISSING: predicates whose
 *     range/set is empty or full are kept as predicates because an absent
 *     attribute still yields MISSING.
 *
 * Sources: each emitted node reuses (unions) the `src` tags of every input
 * node it was derived from, and each fired rule is reported in `lineage`.
 */
import type { AndNode, CmpNode, InNode, Node, OrNode } from "./ast.ts";
import {
  andNode,
  cmpRangesNode,
  constNode,
  inNode,
  notNode,
  orNode,
  sourcesOf,
} from "./ast.ts";
import {
  complementRanges,
  intersectRanges,
  isEmpty,
  unionRanges,
} from "./ranges.ts";
import type { Schema } from "./schema.ts";
import { assertDeclared } from "./schema.ts";

export interface LineageEntry {
  rule: string;
  detail: string;
  sources: string[];
}

export interface SimplifyResult {
  expr: Node;
  lineage: LineageEntry[];
}

export function simplify(expr: Node, schema: Schema): SimplifyResult {
  assertDeclared(expr, schema);
  const lineage: LineageEntry[] = [];
  const out = go(expr, schema, lineage);
  return { expr: out, lineage };
}

function record(lineage: LineageEntry[], rule: string, detail: string, nodes: Node[]): void {
  const sources = [...new Set(nodes.flatMap((n) => (n.src ? [...n.src] : [])))].sort();
  lineage.push({ rule, detail, sources });
}

function go(node: Node, schema: Schema, lineage: LineageEntry[]): Node {
  switch (node.kind) {
    case "const":
    case "bool":
    case "in":
    case "cmp":
      return node;
    case "not":
      return simplifyNot(node.arg, go(node.arg, schema, lineage), schema, lineage);
    case "and":
      return mergeAnd(node.args.map((a) => go(a, schema, lineage)), schema, lineage);
    case "or":
      return mergeOr(node.args.map((a) => go(a, schema, lineage)), schema, lineage);
  }
}

/* ------------------------------- NOT rules ------------------------------- */

function simplifyNot(
  originalArg: Node,
  arg: Node,
  schema: Schema,
  lineage: LineageEntry[],
): Node {
  // NOT true <-> false; NOT false <-> true (constant folding)
  if (arg.kind === "const") {
    const folded = constNode(arg.value === 1 ? 0 : 1, arg.src);
    record(lineage, "not-const", `NOT(${arg.value}) -> ${folded.value}`, [originalArg]);
    return folded;
  }
  // NOT NOT x -> x
  if (arg.kind === "not") {
    record(lineage, "double-negation", "NOT(NOT x) -> x", [originalArg]);
    return arg.arg;
  }
  // Interval complement: NOT (x op v) -> x ∈ complement(range)
  if (arg.kind === "cmp") {
    const comp = cmpRangesNode(arg.name, complementRanges(arg.ranges), arg.src);
    record(lineage, "int-complement", `NOT ${arg.name} condition -> complement interval`, [arg]);
    return comp;
  }
  // Finite-set complement: NOT (x ∈ S) -> x ∈ (U \\ S)
  if (arg.kind === "in") {
    const universe = schema[arg.name];
    if (universe && universe.kind === "finite") {
      const rest = universe.values.filter((v) => !arg.set.has(v));
      const out = inNode(arg.name, rest, undefined);
      out.src = arg.src ? new Set(arg.src) : undefined;
      record(lineage, "set-complement", `NOT (${arg.name} ∈ S) -> ${arg.name} ∈ U\\S`, [arg]);
      return out;
    }
  }
  // De Morgan (sound in strong Kleene logic)
  if (arg.kind === "and") {
    record(lineage, "de-morgan", "NOT(AND) -> OR(NOT...)", [arg]);
    return mergeOr(
      arg.args.map((a) => simplifyNot(a, a, schema, lineage)),
      schema,
      lineage,
    );
  }
  if (arg.kind === "or") {
    record(lineage, "de-morgan", "NOT(OR) -> AND(NOT...)", [arg]);
    return mergeAnd(
      arg.args.map((a) => simplifyNot(a, a, schema, lineage)),
      schema,
      lineage,
    );
  }
  return notNode(arg, sourcesOf(originalArg, arg));
}

/* ------------------------------- AND rules ------------------------------- */

function flatten(args: Node[], kind: "and" | "or"): Node[] {
  const out: Node[] = [];
  for (const a of args) {
    if (a.kind === kind) out.push(...a.args);
    else out.push(a);
  }
  return out;
}

/** Identity constants disappear semantically; keep their provenance on the survivor. */
function absorbSources(term: Node, constants: Node[]): Node {
  const extra = sourcesOf(...constants);
  if (extra.size === 0) return term;
  const merged = sourcesOf(term);
  for (const s of extra) merged.add(s);
  return { ...term, src: merged };
}

function mergeAnd(args: Node[], schema: Schema, lineage: LineageEntry[]): Node {
  const flat = flatten(args, "and");
  const consts = flat.filter((n): n is Node & { kind: "const" } => n.kind === "const");
  let terms = flat.filter((n) => n.kind !== "const");

  // false AND x -> false (keep the false branch's provenance)
  const falseConsts = consts.filter((c) => c.value === 0);
  if (falseConsts.length > 0) {
    const src = sourcesOf(...falseConsts);
    record(lineage, "and-false-annihilator", "false AND ... -> false", falseConsts);
    return constNode(0, src.size ? src : undefined);
  }

  // true AND x -> x
  const trueConsts = consts.filter((c) => c.value === 1);
  if (trueConsts.length > 0) {
    record(lineage, "and-true-identity", "true AND x -> x", trueConsts);
  }

  // interval / finite-set intersection groups on the same attribute
  terms = mergeGroups(terms, "and", schema, lineage);

  // idempotence: x AND x -> x
  terms = dedupe(terms, lineage, "and");

  if (terms.length === 0) return constNode(1, sourcesOf(...trueConsts).size ? sourcesOf(...trueConsts) : undefined);
  if (terms.length === 1) return absorbSources(terms[0], trueConsts);
  return andNode(terms, sourcesOf(...flat));
}

/* -------------------------------- OR rules -------------------------------- */

function mergeOr(args: Node[], schema: Schema, lineage: LineageEntry[]): Node {
  const flat = flatten(args, "or");
  const consts = flat.filter((n): n is Node & { kind: "const" } => n.kind === "const");
  let terms = flat.filter((n) => n.kind !== "const");

  // true OR x -> true
  const trueConsts = consts.filter((c) => c.value === 1);
  if (trueConsts.length > 0) {
    const src = sourcesOf(...trueConsts);
    record(lineage, "or-true-annihilator", "true OR ... -> true", trueConsts);
    return constNode(1, src.size ? src : undefined);
  }

  // false OR x -> x
  const falseConsts = consts.filter((c) => c.value === 0);
  if (falseConsts.length > 0) {
    record(lineage, "or-false-identity", "false OR x -> x", falseConsts);
  }

  terms = mergeGroups(terms, "or", schema, lineage);
  terms = dedupe(terms, lineage, "or");

  if (terms.length === 0) return constNode(0, sourcesOf(...falseConsts).size ? sourcesOf(...falseConsts) : undefined);
  if (terms.length === 1) return absorbSources(terms[0], falseConsts);
  return orNode(terms, sourcesOf(...flat));
}

/* ------------- per-attribute interval / finite-set merging -------------- */

function mergeGroups(terms: Node[], op: "and" | "or", schema: Schema, lineage: LineageEntry[]): Node[] {
  const cmpGroups = new Map<string, CmpNode[]>();
  const inGroups = new Map<string, InNode[]>();
  const rest: Node[] = [];

  for (const t of terms) {
    if (t.kind === "cmp") pushGroup(cmpGroups, t.name, t);
    else if (t.kind === "in") pushGroup(inGroups, t.name, t);
    else rest.push(t);
  }

  for (const [name, group] of cmpGroups) {
    if (group.length < 2) {
      rest.push(group[0]);
      continue;
    }
    const ranges =
      op === "and"
        ? group.slice(1).reduce((acc, g) => intersectRanges(acc, g.ranges), group[0].ranges)
        : group.slice(1).reduce((acc, g) => unionRanges(acc, g.ranges), group[0].ranges);
    const merged = cmpRangesNode(name, ranges, sourcesOf(...group));
    record(
      lineage,
      op === "and" ? "int-intersect" : "int-union",
      `${name}: merged ${group.length} interval(s) -> ${isEmpty(ranges) ? "empty" : ranges.length} box(es)`,
      group,
    );
    rest.push(merged);
  }

  for (const [name, group] of inGroups) {
    if (group.length < 2) {
      rest.push(group[0]);
      continue;
    }
    let values: Set<string>;
    if (op === "and") {
      values = new Set([...group[0].set].filter((v) => group.slice(1).every((g) => g.set.has(v))));
    } else {
      values = new Set(group.flatMap((g) => [...g.set]));
    }
    const universe = schema[name];
    if (universe.kind !== "finite") throw new Error("in predicate on non-finite attribute");
    const merged = inNode(name, values, undefined);
    merged.src = sourcesOf(...group);
    record(
      lineage,
      op === "and" ? "set-intersect" : "set-union",
      `${name}: merged ${group.length} set(s) -> {${[...values].sort().join(",")}}`,
      group,
    );
    rest.push(merged);
  }

  return rest;
}

function pushGroup<K, V>(map: Map<K, V[]>, key: K, value: V): void {
  const list = map.get(key);
  if (list) list.push(value);
  else map.set(key, [value]);
}

/* ----------------------------- x op x -> x ------------------------------ */

function structuralKey(node: Node): string {
  const src = node.src ? [...node.src].sort() : [];
  switch (node.kind) {
    case "const":
      return `const:${node.value}`;
    case "bool":
      return `bool:${node.name}`;
    case "in":
      return `in:${node.name}:{${[...node.set].sort().join(",")}}`;
    case "cmp":
      return `cmp:${node.name}:${node.ranges.map((r) => `${r.lo}..${r.hi}`).join("|")}`;
    case "not":
      return `not:${structuralKey(node.arg)}`;
    case "and":
      return `and:${node.args.map(structuralKey).sort().join("&")}`;
    case "or":
      return `or:${node.args.map(structuralKey).sort().join("|")}`;
  }
}

function dedupe(terms: Node[], lineage: LineageEntry[], op: "and" | "or"): Node[] {
  const seen = new Map<string, Node>();
  for (const t of terms) {
    const key = structuralKey(t);
    const prev = seen.get(key);
    if (prev) {
      const mergedSrc = sourcesOf(prev, t);
      const merged: Node = { ...prev, src: mergedSrc.size ? mergedSrc : prev.src };
      seen.set(key, merged);
      record(lineage, op === "and" ? "and-idempotence" : "or-idempotence", "x op x -> x", [prev, t]);
    } else {
      seen.set(key, t);
    }
  }
  return [...seen.values()];
}

/* ------------------------------ introspection ---------------------------- */

export function allSources(node: Node): Set<string> {
  const out = new Set<string>();
  const walk = (n: Node): void => {
    if (n.src) for (const s of n.src) out.add(s);
    if (n.kind === "not") walk(n.arg);
    if (n.kind === "and" || n.kind === "or") n.args.forEach(walk);
  };
  walk(node);
  return out;
}

export type { AndNode, OrNode };
