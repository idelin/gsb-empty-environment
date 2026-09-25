/**
 * Expression AST for the policy language.
 *
 * Policy may read only:
 *   - explicitly declared finite-set (enum) attributes      -> In
 *   - explicitly declared integer attributes                -> Cmp
 *   - boolean attributes                                    -> BoolRef
 * and combine them with AND / OR / NOT.
 *
 * Every node carries an optional `src` (source span/identifier) used for
 * provenance. Simplified nodes reuse the sources of the terms they were
 * derived from so lineage is preserved.
 */

export type Op = "<" | "<=" | ">" | ">=" | "==" | "!=";

/** Integer interval [lo, hi]; lo/hi may be +/- Infinity (a half line). */
export interface Interval {
  lo: number;
  hi: number;
}

export interface NodeBase {
  src?: ReadonlySet<string>;
}

export interface ConstNode extends NodeBase {
  kind: "const";
  value: 0 | 1; // FALSE | TRUE (constants are never MISSING)
}

/** A declared boolean attribute. Evaluates to MISSING when absent. */
export interface BoolRefNode extends NodeBase {
  kind: "bool";
  name: string;
}

/** Membership test: value of enum attribute ∈ set. */
export interface InNode extends NodeBase {
  kind: "in";
  name: string;
  set: ReadonlySet<string>;
  /**
   * True for predicates produced by algebra (complement / merge). Such a
   * predicate must still yield MISSING when the attribute is absent, even if
   * its set covers the whole declared universe.
   */
  requiresPresence?: boolean;
}

/**
 * Integer condition over `name`.
 *
 * `ranges` is the set of integers for which the predicate is TRUE, expressed
 * as disjoint, sorted [lo, hi] boxes on the extended integer line. `op`/
 * `value` retain the originating spelling for display; after interval merging
 * they become "in" with a synthetic value and `ranges` is the source of truth.
 */
export interface CmpNode extends NodeBase {
  kind: "cmp";
  name: string;
  op: Op | "in";
  value: number | null;
  ranges: Interval[];
  /** See InNode.requiresPresence. */
  requiresPresence?: boolean;
}

export interface NotNode extends NodeBase {
  kind: "not";
  arg: Node;
}

export interface AndNode extends NodeBase {
  kind: "and";
  args: Node[];
}

export interface OrNode extends NodeBase {
  kind: "or";
  args: Node[];
}

export type Node =
  | ConstNode
  | BoolRefNode
  | InNode
  | CmpNode
  | NotNode
  | AndNode
  | OrNode;

/* ---------- constructors with provenance bookkeeping ---------- */

let counter = 0;
export function freshSrc(label: string): string {
  counter += 1;
  return `${label}#${counter}`;
}

export function sourcesOf(...nodes: Node[]): Set<string> {
  const out = new Set<string>();
  for (const n of nodes) {
    if (n.src) for (const s of n.src) out.add(s);
  }
  return out;
}

export function constNode(value: 0 | 1, src?: ReadonlySet<string>): ConstNode {
  return { kind: "const", value, src: src ? new Set(src) : undefined };
}

export function boolRef(name: string, src?: string): BoolRefNode {
  return { kind: "bool", name, src: src ? new Set([src]) : undefined };
}

export function inNode(
  name: string,
  values: Iterable<string>,
  src?: string,
): InNode {
  return {
    kind: "in",
    name,
    set: new Set(values),
    src: src ? new Set([src]) : undefined,
  };
}

import { normalizeRanges } from "./ranges.ts";

const RANGES_OF: Record<Op, (v: number) => Interval[]> = {
  "<": (v) => [{ lo: -Infinity, hi: v - 1 }],
  "<=": (v) => [{ lo: -Infinity, hi: v }],
  ">": (v) => [{ lo: v + 1, hi: Infinity }],
  ">=": (v) => [{ lo: v, hi: Infinity }],
  "==": (v) => [{ lo: v, hi: v }],
  "!=": (v) => [{ lo: -Infinity, hi: v - 1 }, { lo: v + 1, hi: Infinity }],
};

export function cmpNode(name: string, op: Op, value: number, src?: string): CmpNode {
  return {
    kind: "cmp",
    name,
    op,
    value,
    ranges: normalizeRanges(RANGES_OF[op](value)),
    src: src ? new Set([src]) : undefined,
  };
}

export function cmpRangesNode(
  name: string,
  ranges: Interval[],
  src?: ReadonlySet<string>,
  requiresPresence = false,
): CmpNode {
  return {
    kind: "cmp",
    name,
    op: "in",
    value: null,
    ranges: normalizeRanges(ranges),
    src: src ? new Set(src) : undefined,
    requiresPresence,
  };
}

export function notNode(arg: Node, src?: ReadonlySet<string>): NotNode {
  return { kind: "not", arg, src: src ? new Set(src) : undefined };
}

export function andNode(args: Node[], src?: ReadonlySet<string>): AndNode {
  return { kind: "and", args, src: src ? new Set(src) : undefined };
}

export function orNode(args: Node[], src?: ReadonlySet<string>): OrNode {
  return { kind: "or", args, src: src ? new Set(src) : undefined };
}

/* ---------- readable rendering ---------- */

function srcLabel(n: Node): string {
  return n.src && n.src.size > 0 ? ` [${[...n.src].sort().join(",")}]` : "";
}

export function render(node: Node): string {
  switch (node.kind) {
    case "const":
      return `${node.value === 1 ? "true" : "false"}${srcLabel(node)}`;
    case "bool":
      return `${node.name}${srcLabel(node)}`;
    case "in":
      return `${node.name}∈{${[...node.set].sort().join(",")}}${srcLabel(node)}`;
    case "cmp":
      if (node.op === "in") return `${node.name}∈{${node.ranges.map((r) => `${r.lo}..${r.hi}`).join(",")}}${srcLabel(node)}`;
      return `${node.name}${node.op}${node.value}${srcLabel(node)}`;
    case "not":
      return `NOT(${render(node.arg)})${srcLabel(node)}`;
    case "and":
      return `(${node.args.map(render).join(" AND ")})${srcLabel(node)}`;
    case "or":
      return `(${node.args.map(render).join(" OR ")})${srcLabel(node)}`;
  }
}
