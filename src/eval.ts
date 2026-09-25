/**
 * Evaluation in two flavors:
 *
 * - `evalPoint`: exact three-valued evaluation of a condition on one concrete
 *   assignment. An absent attribute (UNKNOWN) makes every atom that reads it
 *   "unknown"; boolean connectives use Kleene's strong three-valued logic.
 *
 * - `evalBox`: sound approximation of a condition over a whole box (product
 *   of intervals / value lists) using interval arithmetic. Returns "T" when
 *   every point in the box satisfies the condition, "F" when no point does,
 *   and "M" (mixed) otherwise. This is what lets the containment engine
 *   reason about whole interval ranges without enumerating values.
 */

import type { CmpOp, Cond, Scalar } from "./conditions.ts";
import { UNKNOWN, type Assignment, type AttrBox, type Box } from "./domain.ts";

export type PointVal = "true" | "false" | "unknown";
export type Tri = "T" | "F" | "M";

function cmpNumber(lhs: number, op: CmpOp, rhs: number): boolean {
  switch (op) {
    case "<":
      return lhs < rhs;
    case "<=":
      return lhs <= rhs;
    case "==":
      return lhs === rhs;
    case "!=":
      return lhs !== rhs;
    case ">=":
      return lhs >= rhs;
    case ">":
      return lhs > rhs;
  }
}

/** Truth of `x <op> k` for every x in [lo, hi], approximated as a Tri. */
export function cmpRange(lo: number, hi: number, op: CmpOp, k: number): Tri {
  const loHolds = cmpNumber(lo, op, k);
  const hiHolds = cmpNumber(hi, op, k);
  if (loHolds && hiHolds) {
    if (op === "==") return "T"; // lo == hi == k
    if (op === "!=") {
      // Both endpoints differ from k; the whole range differs from k only
      // when k lies outside [lo, hi].
      return k < lo || k > hi ? "T" : "M";
    }
    return "T";
  }
  if (!loHolds && !hiHolds) {
    if (op === "==") return k > lo && k < hi ? "M" : "F";
    if (op === "!=") return "F"; // lo == hi == k
    return "F";
  }
  return "M";
}

function evalAtomPoint(c: Cond, a: Assignment): PointVal {
  switch (c.tag) {
    case "true":
      return "true";
    case "false":
      return "false";
    case "in": {
      const v = a[c.attr];
      if (v === UNKNOWN) return "unknown";
      return c.values.includes(v as Scalar) ? "true" : "false";
    }
    case "cmp": {
      const v = a[c.attr];
      if (v === UNKNOWN) return "unknown";
      return cmpNumber(v as number, c.op, c.value) ? "true" : "false";
    }
    case "diff": {
      const l = a[c.left];
      const r = a[c.right];
      if (l === UNKNOWN || r === UNKNOWN) return "unknown";
      return cmpNumber((l as number) - (r as number), c.op, c.value)
        ? "true"
        : "false";
    }
    case "setHas": {
      const v = a[c.attr];
      if (v === UNKNOWN) return "unknown";
      return (v as readonly Scalar[]).includes(c.elem) ? "true" : "false";
    }
    case "setSubset": {
      const v = a[c.attr];
      if (v === UNKNOWN) return "unknown";
      return (v as readonly Scalar[]).every((x) => c.values.includes(x))
        ? "true"
        : "false";
    }
    case "setIntersects": {
      const v = a[c.attr];
      if (v === UNKNOWN) return "unknown";
      return (v as readonly Scalar[]).some((x) => c.values.includes(x))
        ? "true"
        : "false";
    }
    default:
      throw new Error("not an atom");
  }
}

export function evalPoint(c: Cond, a: Assignment): PointVal {
  switch (c.tag) {
    case "not": {
      const v = evalPoint(c.of, a);
      if (v === "unknown") return "unknown";
      return v === "true" ? "false" : "true";
    }
    case "and": {
      let unknown = false;
      for (const sub of c.args) {
        const v = evalPoint(sub, a);
        if (v === "false") return "false";
        if (v === "unknown") unknown = true;
      }
      return unknown ? "unknown" : "true";
    }
    case "or": {
      let unknown = false;
      for (const sub of c.args) {
        const v = evalPoint(sub, a);
        if (v === "true") return "true";
        if (v === "unknown") unknown = true;
      }
      return unknown ? "unknown" : "false";
    }
    default:
      return evalAtomPoint(c, a);
  }
}

function evalAtomBox(c: Cond, box: Box): Tri {
  switch (c.tag) {
    case "true":
      return "T";
    case "false":
      return "F";
    case "in": {
      const b: AttrBox = box[c.attr];
      if (b.kind === "int") {
        const hits = c.values.filter(
          (v) => typeof v === "number" && v >= b.lo && v <= b.hi,
        );
        if (hits.length === 0) return "F";
        return b.lo === b.hi && hits.length === 1 ? "T" : "M";
      }
      if (b.kind !== "enum") throw new Error(`in() on ${b.kind} attribute`);
      const remaining = b.values.filter((v) => c.values.includes(v));
      if (remaining.length === 0) return "F";
      return remaining.length === b.values.length ? "T" : "M";
    }
    case "cmp": {
      const b: AttrBox = box[c.attr];
      if (b.kind !== "int") throw new Error(`cmp() on ${b.kind} attribute`);
      return cmpRange(b.lo, b.hi, c.op, c.value);
    }
    case "diff": {
      const bl: AttrBox = box[c.left];
      const br: AttrBox = box[c.right];
      if (bl.kind !== "int" || br.kind !== "int") {
        throw new Error("diff() requires int attributes");
      }
      return cmpRange(bl.lo - br.hi, bl.hi - br.lo, c.op, c.value);
    }
    case "setHas": {
      const b: AttrBox = box[c.attr];
      if (b.kind !== "set") throw new Error(`setHas() on ${b.kind} attribute`);
      const withElem = b.sets.filter((s) => s.includes(c.elem));
      if (withElem.length === 0) return "F";
      return withElem.length === b.sets.length ? "T" : "M";
    }
    case "setSubset": {
      const b: AttrBox = box[c.attr];
      if (b.kind !== "set") {
        throw new Error(`setSubset() on ${b.kind} attribute`);
      }
      const ok = b.sets.filter((s) => s.every((x) => c.values.includes(x)));
      if (ok.length === 0) return "F";
      return ok.length === b.sets.length ? "T" : "M";
    }
    case "setIntersects": {
      const b: AttrBox = box[c.attr];
      if (b.kind !== "set") {
        throw new Error(`setIntersects() on ${b.kind} attribute`);
      }
      const ok = b.sets.filter((s) => s.some((x) => c.values.includes(x)));
      if (ok.length === 0) return "F";
      return ok.length === b.sets.length ? "T" : "M";
    }
    default:
      throw new Error("not an atom");
  }
}

export function evalBox(c: Cond, box: Box): Tri {
  switch (c.tag) {
    case "not": {
      const v = evalBox(c.of, box);
      if (v === "T") return "F";
      if (v === "F") return "T";
      return "M";
    }
    case "and": {
      let mixed = false;
      for (const sub of c.args) {
        const v = evalBox(sub, box);
        if (v === "F") return "F";
        if (v === "M") mixed = true;
      }
      return mixed ? "M" : "T";
    }
    case "or": {
      let mixed = false;
      for (const sub of c.args) {
        const v = evalBox(sub, box);
        if (v === "T") return "T";
        if (v === "M") mixed = true;
      }
      return mixed ? "M" : "F";
    }
    default:
      return evalAtomBox(c, box);
  }
}
