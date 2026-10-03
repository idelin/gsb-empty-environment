/**
 * Counterfactual confirmation: take a located root-cause rule, substitute
 * the other pipeline's rule into one pipeline, re-evaluate independently,
 * and check whether the outputs then agree exactly. If they do, the rule
 * is confirmed as the cause; if not, it is at most a partial cause.
 */
import { evaluate, type Node, type Pipeline } from "./dag.ts";
import { comparePipelines, type RuleDiff } from "./compare.ts";
import { Rational } from "./rational.ts";
import { lookupUnit, SI_TABLE } from "./units.ts";
import type { RoundingMode } from "./rounding.ts";

export type Substitution =
  | { type: "rounding"; nodeId: string; places: number; mode: RoundingMode; unit?: string }
  | { type: "const"; nodeId: string; value: string }
  | { type: "convert-target"; nodeId: string; unit: string }
  | { type: "unit-factor"; unit: string; scale: string }
  | { type: "bypass-round"; nodeId: string };

export function describeSubstitution(s: Substitution): string {
  switch (s.type) {
    case "rounding":
      return `set ${s.nodeId} rounding to places=${s.places}, mode=${s.mode}`;
    case "const":
      return `set ${s.nodeId} constant to ${s.value}`;
    case "convert-target":
      return `set ${s.nodeId} convert target to ${s.unit}`;
    case "unit-factor":
      return `set unit ${s.unit} factor to ${s.scale}`;
    case "bypass-round":
      return `bypass rounding stage ${s.nodeId}`;
  }
}

export function applySubstitution(p: Pipeline, sub: Substitution): Pipeline {
  if (sub.type === "unit-factor") {
    const table = { ...(p.unitTable ?? SI_TABLE) };
    const def = lookupUnit(table, sub.unit);
    table[sub.unit] = { ...def, scale: Rational.parse(sub.scale) };
    return { ...p, unitTable: table };
  }
  if (sub.type === "bypass-round") {
    // Replace `round(x)` by its input, rewiring consumers.
    const roundNode = p.nodes.find((n) => n.id === sub.nodeId);
    if (!roundNode || roundNode.op !== "round") {
      throw new Error(`bypass-round: ${sub.nodeId} is not a round node`);
    }
    const src = roundNode.a;
    const nodes: Node[] = [];
    for (const n of p.nodes) {
      if (n.id === sub.nodeId) continue;
      nodes.push(rewire(n, sub.nodeId, src));
    }
    return { ...p, nodes };
  }
  const nodes = p.nodes.map((n): Node => {
    if (n.id !== sub.nodeId) return n;
    switch (sub.type) {
      case "rounding":
        if (n.op !== "round") throw new Error(`${n.id} is not a round node`);
        return { ...n, places: sub.places, mode: sub.mode, unit: sub.unit ?? n.unit };
      case "const":
        if (n.op !== "const") throw new Error(`${n.id} is not a const node`);
        return { ...n, value: sub.value };
      case "convert-target":
        if (n.op !== "convert") throw new Error(`${n.id} is not a convert node`);
        return { ...n, unit: sub.unit };
    }
  });
  return { ...p, nodes };
}

function rewire(n: Node, from: string, to: string): Node {
  const fix = (x: string): string => (x === from ? to : x);
  switch (n.op) {
    case "add":
    case "sub":
    case "mul":
    case "div":
      return { ...n, a: fix(n.a), b: fix(n.b) };
    case "neg":
    case "round":
    case "convert":
    case "output":
      return { ...n, a: fix(n.a) };
    default:
      return n;
  }
}

/**
 * Derive the substitution that would make pipeline A use pipeline B's rule
 * for one located root cause.
 */
export function substitutionFor(
  cause: RuleDiff,
  pa: Pipeline,
  pb: Pipeline
): Substitution | null {
  const nodeB = cause.bNode ? pb.nodes.find((n) => n.id === cause.bNode) : undefined;
  switch (cause.kind) {
    case "rounding": {
      if (!nodeB || nodeB.op !== "round" || !cause.aNode) return null;
      return {
        type: "rounding",
        nodeId: cause.aNode,
        places: nodeB.places,
        mode: nodeB.mode,
        unit: nodeB.unit,
      };
    }
    case "constant":
    case "denominator": {
      if (!nodeB || nodeB.op !== "const" || !cause.aNode) return null;
      return { type: "const", nodeId: cause.aNode, value: nodeB.value };
    }
    case "unit-conversion-factor": {
      const aNode = cause.aNode ? pa.nodes.find((n) => n.id === cause.aNode) : undefined;
      const unitOf = (n: Node | undefined): string | undefined =>
        n && (n.op === "const" || n.op === "input") ? n.unit : undefined;
      const unit = unitOf(aNode) ?? unitOf(nodeB);
      if (!unit) return null;
      const scale = lookupUnit(pb.unitTable ?? SI_TABLE, unit).scale.toString();
      return { type: "unit-factor", unit, scale };
    }
    case "unit-target": {
      if (!nodeB || nodeB.op !== "convert" || !cause.aNode) return null;
      return { type: "convert-target", nodeId: cause.aNode, unit: nodeB.unit };
    }
    case "structural": {
      if (cause.aNode) {
        const n = pa.nodes.find((x) => x.id === cause.aNode);
        if (n && n.op === "round") return { type: "bypass-round", nodeId: n.id };
      }
      return null;
    }
    case "unit-dimension":
      return null;
  }
}

export interface CounterfactualResult {
  cause: RuleDiff;
  substitution: Substitution | null;
  description: string;
  /** Outputs of modified A vs B, compared exactly and independently. */
  confirmed: boolean | "not-applicable";
  outputsAfter?: Record<string, { modifiedA: string; b: string; equal: boolean }>;
}

export function runCounterfactuals(
  pa: Pipeline,
  pb: Pipeline,
  inputs: Record<string, string | Rational>,
  causes: RuleDiff[]
): CounterfactualResult[] {
  const results: CounterfactualResult[] = [];
  for (const cause of causes) {
    const sub = substitutionFor(cause, pa, pb);
    if (!sub) {
      results.push({
        cause,
        substitution: null,
        description: "no automatic substitution for this cause kind",
        confirmed: "not-applicable",
      });
      continue;
    }
    const modified = applySubstitution(pa, sub);
    // Independent recomputation: fresh evaluation of the modified pipeline.
    const ea = evaluate(modified, inputs);
    const eb = evaluate(pb, inputs);
    const outputsAfter: CounterfactualResult["outputsAfter"] = {};
    let allEqual = true;
    const names = new Set([...ea.outputs.keys(), ...eb.outputs.keys()]);
    for (const name of names) {
      const oa = ea.outputs.get(name);
      const ob = eb.outputs.get(name);
      const equal =
        !!oa && !!ob && oa.value.equals(ob.value) &&
        JSON.stringify(oa.dims) === JSON.stringify(ob.dims);
      outputsAfter[name] = {
        modifiedA: oa ? oa.value.toString() : "<missing>",
        b: ob ? ob.value.toString() : "<missing>",
        equal,
      };
      if (!equal) allEqual = false;
    }
    results.push({
      cause,
      substitution: sub,
      description: describeSubstitution(sub),
      confirmed: allEqual,
      outputsAfter,
    });
  }
  return results;
}

/** Convenience: compare, then run counterfactuals for every root cause. */
export function compareAndConfirm(
  pa: Pipeline,
  pb: Pipeline,
  inputs: Record<string, string | Rational>
): { report: ReturnType<typeof comparePipelines>; counterfactuals: CounterfactualResult[] } {
  const report = comparePipelines(pa, pb, inputs);
  const counterfactuals = runCounterfactuals(pa, pb, inputs, report.rootCauses);
  return { report, counterfactuals };
}
