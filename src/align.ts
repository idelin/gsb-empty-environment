/**
 * DAG alignment. Two pipelines may use different but equivalent
 * intermediate steps, so we never compare "node #i" against "node #i".
 * Instead each node is reduced to a canonical shape term:
 *
 *   - sub(a,b)  -> add(a, neg(b))
 *   - add/mul   -> flattened (associativity) and sorted (commutativity)
 *   - neg(neg(x)) -> x
 *   - const     -> shape ignores the VALUE, keeps the dimension
 *   - round     -> shape ignores places/mode (they are rules, not shape)
 *   - convert   -> shape ignores the target unit
 *
 * Nodes whose canonical shapes match are aligned; outputs additionally
 * align by output name and inputs by slot (provenance), even when shapes
 * differ, so a structural difference cannot hide the final comparison.
 */
import type { Node, Pipeline } from "./dag.ts";
import { dimsKey, lookupUnit, SI_TABLE, type Dims } from "./units.ts";

type Term =
  | { kind: "const"; dims: string }
  | { kind: "input"; slot: string; dims: string }
  | { kind: "add"; parts: Term[] }
  | { kind: "mul"; parts: Term[] }
  | { kind: "neg"; of: Term }
  | { kind: "div"; num: Term; den: Term }
  | { kind: "round"; of: Term }
  | { kind: "convert"; of: Term }
  | { kind: "output"; name: string; of: Term };

function termKey(t: Term): string {
  switch (t.kind) {
    case "const":
      return `const:${t.dims}`;
    case "input":
      return `input:${t.slot}:${t.dims}`;
    case "add":
      return `add(${t.parts.map(termKey).join(",")})`;
    case "mul":
      return `mul(${t.parts.map(termKey).join(",")})`;
    case "neg":
      return `neg(${termKey(t.of)})`;
    case "div":
      return `div(${termKey(t.num)},${termKey(t.den)})`;
    case "round":
      return `round(${termKey(t.of)})`;
    case "convert":
      return `convert(${termKey(t.of)})`;
    case "output":
      return `output:${t.name}(${termKey(t.of)})`;
  }
}

function nodeDims(n: Node, p: Pipeline): string {
  const table = p.unitTable ?? SI_TABLE;
  const unit = n.op === "const" || n.op === "input" ? n.unit ?? "1" : "1";
  const d: Dims = lookupUnit(table, unit).dims;
  return dimsKey(d);
}

/** Canonical shape term per node id, built bottom-up with hash-consing. */
export function shapeTerms(p: Pipeline): Map<string, Term> {
  const byId = new Map<string, Node>();
  for (const n of p.nodes) byId.set(n.id, n);
  const memo = new Map<string, Term>();

  const build = (id: string): Term => {
    const hit = memo.get(id);
    if (hit) return hit;
    const n = byId.get(id);
    if (!n) throw new Error(`unknown node ${id}`);
    let t: Term;
    switch (n.op) {
      case "const":
        t = { kind: "const", dims: nodeDims(n, p) };
        break;
      case "input":
        t = { kind: "input", slot: n.slot, dims: nodeDims(n, p) };
        break;
      case "add":
        t = canonAdd([build(n.a), build(n.b)]);
        break;
      case "sub":
        t = canonAdd([build(n.a), canonNeg(build(n.b))]);
        break;
      case "mul":
        t = canonMul([build(n.a), build(n.b)]);
        break;
      case "div":
        t = { kind: "div", num: build(n.a), den: build(n.b) };
        break;
      case "neg":
        t = canonNeg(build(n.a));
        break;
      case "round":
        t = { kind: "round", of: build(n.a) };
        break;
      case "convert":
        t = { kind: "convert", of: build(n.a) };
        break;
      case "output":
        t = { kind: "output", name: n.name, of: build(n.a) };
        break;
    }
    memo.set(id, t);
    return t;
  };

  for (const n of p.nodes) build(n.id);
  return memo;
}

function flatten(kind: "add" | "mul", parts: Term[]): Term[] {
  const out: Term[] = [];
  for (const p of parts) {
    if (p.kind === kind) out.push(...p.parts);
    else out.push(p);
  }
  return out.sort((x, y) => (termKey(x) < termKey(y) ? -1 : 1));
}

function canonAdd(parts: Term[]): Term {
  const flat = flatten("add", parts);
  return flat.length === 1 ? flat[0] : { kind: "add", parts: flat };
}

function canonMul(parts: Term[]): Term {
  const flat = flatten("mul", parts);
  return flat.length === 1 ? flat[0] : { kind: "mul", parts: flat };
}

function canonNeg(t: Term): Term {
  return t.kind === "neg" ? t.of : { kind: "neg", of: t };
}

export interface AlignedPair {
  aId: string;
  bId: string;
  /** "shape" = structurally equivalent; "provenance" = aligned by name/slot. */
  via: "shape" | "provenance";
  key: string;
}

export interface Alignment {
  pairs: AlignedPair[];
  unmatchedA: string[];
  unmatchedB: string[];
}

export function alignPipelines(a: Pipeline, b: Pipeline): Alignment {
  const termsA = shapeTerms(a);
  const termsB = shapeTerms(b);

  const indexOf = (terms: Map<string, Term>): Map<string, string[]> => {
    const idx = new Map<string, string[]>();
    for (const [id, t] of terms) {
      const k = termKey(t);
      const arr = idx.get(k) ?? [];
      arr.push(id);
      idx.set(k, arr);
    }
    return idx;
  };

  const idxA = indexOf(termsA);
  const idxB = indexOf(termsB);
  const pairs: AlignedPair[] = [];
  const usedA = new Set<string>();
  const usedB = new Set<string>();

  // 1) structural alignment by canonical shape
  for (const [key, idsA] of idxA) {
    const idsB = idxB.get(key) ?? [];
    const n = Math.min(idsA.length, idsB.length);
    for (let i = 0; i < n; i++) {
      pairs.push({ aId: idsA[i], bId: idsB[i], via: "shape", key });
      usedA.add(idsA[i]);
      usedB.add(idsB[i]);
    }
  }

  // 2) provenance alignment: outputs by name, inputs by slot
  const provenanceKey = (n: Node): string | null => {
    if (n.op === "output") return `out:${n.name}`;
    if (n.op === "input") return `in:${n.slot}`;
    return null;
  };
  const provB = new Map<string, string>();
  for (const n of b.nodes) {
    const k = provenanceKey(n);
    if (k && !usedB.has(n.id)) provB.set(k, n.id);
  }
  for (const n of a.nodes) {
    const k = provenanceKey(n);
    if (!k || usedA.has(n.id)) continue;
    const bId = provB.get(k);
    if (bId && !usedB.has(bId)) {
      pairs.push({ aId: n.id, bId, via: "provenance", key: k });
      usedA.add(n.id);
      usedB.add(bId);
    }
  }

  const orderA = new Map(a.nodes.map((n, i) => [n.id, i]));
  pairs.sort((x, y) => (orderA.get(x.aId) ?? 0) - (orderA.get(y.aId) ?? 0));

  return {
    pairs,
    unmatchedA: a.nodes.map((n) => n.id).filter((id) => !usedA.has(id)),
    unmatchedB: b.nodes.map((n) => n.id).filter((id) => !usedB.has(id)),
  };
}
