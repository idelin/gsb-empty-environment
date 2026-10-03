/**
 * Divergence localization on aligned DAGs. Values are compared exactly
 * (Rational equality + dimension equality). No tolerance anywhere, so a
 * unit error can never be hidden by a "close enough" number.
 */
import { evaluate, type EvalResult, type Node, type Pipeline } from "./dag.ts";
import { alignPipelines, type Alignment } from "./align.ts";
import { Rational } from "./rational.ts";
import { dimsKey, lookupUnit, SI_TABLE } from "./units.ts";
import type { RoundingMode } from "./rounding.ts";

export type RuleKind =
  | "rounding" // rounding mode / places differ
  | "constant" // plain constant value differs
  | "unit-conversion-factor" // same unit name, different exact factor
  | "unit-dimension" // dimension mismatch / unit error in trace
  | "unit-target" // convert-to different target units
  | "denominator" // different divisor convention (e.g. N vs N-1)
  | "structural"; // one pipeline has a stage the other lacks

export interface RuleDiff {
  kind: RuleKind;
  aNode: string | null; // null when the stage only exists in B
  bNode: string | null;
  detail: string;
  aValue?: string; // exact evaluated values at the divergence locus
  bValue?: string;
}

export interface ChainStep {
  aId: string;
  bId: string;
  op: string;
  aValue: string; // exact
  bValue: string; // exact
  delta: string; // exact b - a
  dims: string;
  unitError?: string;
}

export interface CompareReport {
  status:
    | "identical"
    | "intermediate-divergence" // finals equal, intermediates differ
    | "output-divergence"
    | "cannot-locate"; // outputs differ but intermediate evidence missing
  inputs: Record<string, string>;
  alignedPairs: number;
  unmatchedA: string[];
  unmatchedB: string[];
  firstDivergence?: ChainStep;
  rootCauses: RuleDiff[];
  /** One exact chain per divergent output, root cause -> output. */
  differenceChains: { output: string; steps: ChainStep[] }[];
  divergentOutputs: { name: string; aValue: string; bValue: string; delta: string }[];
  /** Aligned pairs whose trace was not retained on at least one side. */
  missingEvidence: { aId: string; bId: string }[];
  notes: string[];
}

interface PairEvidence {
  aId: string;
  bId: string;
  aNode: Node;
  bNode: Node;
  divergent: boolean;
  step?: ChainStep; // present when both traces exist
}

function nodeInputs(n: Node): string[] {
  switch (n.op) {
    case "const":
    case "input":
      return [];
    case "neg":
    case "round":
    case "convert":
    case "output":
      return [n.a];
    default:
      return [n.a, n.b];
  }
}

function classifyConstDiff(a: Node, b: Node, pa: Pipeline, pb: Pipeline): RuleKind {
  const ua = a.op === "const" ? a.unit : undefined;
  const ub = b.op === "const" ? b.unit : undefined;
  if (ua && ub && ua === ub) {
    const ta = pa.unitTable ?? SI_TABLE;
    const tb = pb.unitTable ?? SI_TABLE;
    if (!lookupUnit(ta, ua).scale.equals(lookupUnit(tb, ub).scale)) {
      return "unit-conversion-factor";
    }
  }
  if (ua !== ub) return "unit-dimension";
  return "constant";
}

/** Describe the local "rule" difference between two aligned nodes, if any. */
export function localRuleDiff(
  aNode: Node,
  bNode: Node,
  pa: Pipeline,
  pb: Pipeline,
  consumersA: Map<string, Node[]>
): RuleDiff | null {
  if (aNode.op !== bNode.op) {
    return {
      kind: "structural",
      aNode: aNode.id,
      bNode: bNode.id,
      detail: `aligned by provenance but op differs: A.${aNode.op} vs B.${bNode.op}`,
    };
  }
  switch (aNode.op) {
    case "const": {
      const bn = bNode as Node & { op: "const" };
      const sameUnit = (aNode.unit ?? "1") === (bn.unit ?? "1");
      const factorDiff =
        sameUnit &&
        !lookupUnit(pa.unitTable ?? SI_TABLE, aNode.unit ?? "1").scale.equals(
          lookupUnit(pb.unitTable ?? SI_TABLE, bn.unit ?? "1").scale
        );
      if (aNode.value !== bn.value || !sameUnit || factorDiff) {
        let kind = classifyConstDiff(aNode, bNode, pa, pb);
        if (kind === "constant") {
          const usedAsDivisor = (consumersA.get(aNode.id) ?? []).some(
            (c) => (c.op === "div" && c.b === aNode.id)
          );
          if (usedAsDivisor) kind = "denominator";
        }
        return {
          kind,
          aNode: aNode.id,
          bNode: bNode.id,
          detail:
            `const ${aNode.label ?? aNode.id}: ` +
            `A=${aNode.value}${aNode.unit ? " " + aNode.unit : ""} vs ` +
            `B=${bn.value}${bn.unit ? " " + bn.unit : ""}`,
        };
      }
      return null;
    }
    case "input": {
      const bn = bNode as Node & { op: "input" };
      if ((aNode.unit ?? "1") !== (bn.unit ?? "1")) {
        return {
          kind: "unit-dimension",
          aNode: aNode.id,
          bNode: bNode.id,
          detail: `input ${aNode.slot}: A unit ${aNode.unit ?? "1"} vs B unit ${bn.unit ?? "1"}`,
        };
      }
      const unit = aNode.unit ?? "1";
      const sa = lookupUnit(pa.unitTable ?? SI_TABLE, unit).scale;
      const sb = lookupUnit(pb.unitTable ?? SI_TABLE, unit).scale;
      if (!sa.equals(sb)) {
        return {
          kind: "unit-conversion-factor",
          aNode: aNode.id,
          bNode: bNode.id,
          detail: `input ${aNode.slot}: unit ${unit} factor A=${sa} vs B=${sb}`,
        };
      }
      return null;
    }
    case "round": {
      const bn = bNode as Node & { op: "round" };
      const diffs: string[] = [];
      if (aNode.places !== bn.places) diffs.push(`places A=${aNode.places} B=${bn.places}`);
      if (aNode.mode !== bn.mode) diffs.push(`mode A=${aNode.mode} B=${bn.mode}`);
      if ((aNode.unit ?? "") !== (bn.unit ?? ""))
        diffs.push(`unit A=${aNode.unit ?? "-"} B=${bn.unit ?? "-"}`);
      if (diffs.length === 0) return null;
      return {
        kind: "rounding",
        aNode: aNode.id,
        bNode: bNode.id,
        detail: `round ${aNode.id}: ${diffs.join("; ")}`,
      };
    }
    case "convert": {
      const bn = bNode as Node & { op: "convert" };
      if (aNode.unit !== bn.unit) {
        return {
          kind: "unit-target",
          aNode: aNode.id,
          bNode: bNode.id,
          detail: `convert ${aNode.id}: A->${aNode.unit} vs B->${bn.unit}`,
        };
      }
      return null;
    }
    default:
      return null;
  }
}

export function comparePipelines(
  pa: Pipeline,
  pb: Pipeline,
  inputs: Record<string, string | Rational> = {},
  opts: { keepTraceA?: (id: string) => boolean; keepTraceB?: (id: string) => boolean } = {}
): CompareReport {
  const ea = evaluate(pa, inputs, { keepTrace: opts.keepTraceA });
  const eb = evaluate(pb, inputs, { keepTrace: opts.keepTraceB });
  const alignment = alignPipelines(pa, pb);
  return analyze(pa, pb, ea, eb, alignment, inputs);
}

export function analyze(
  pa: Pipeline,
  pb: Pipeline,
  ea: EvalResult,
  eb: EvalResult,
  alignment: Alignment,
  inputs: Record<string, string | Rational>
): CompareReport {
  const nodesA = new Map(pa.nodes.map((n) => [n.id, n]));
  const nodesB = new Map(pb.nodes.map((n) => [n.id, n]));
  const consumersA = new Map<string, Node[]>();
  for (const n of pa.nodes) {
    for (const up of nodeInputs(n)) {
      const arr = consumersA.get(up) ?? [];
      arr.push(n);
      consumersA.set(up, arr);
    }
  }

  const inputStrings: Record<string, string> = {};
  for (const [k, v] of Object.entries(inputs)) {
    inputStrings[k] = typeof v === "string" ? v : v.toString();
  }

  const missingEvidence: { aId: string; bId: string }[] = [];
  const pairs: PairEvidence[] = [];
  const divergentPairIds = new Set<string>(); // keyed by aId

  for (const pair of alignment.pairs) {
    const aNode = nodesA.get(pair.aId)!;
    const bNode = nodesB.get(pair.bId)!;
    const ta = ea.traces.get(pair.aId);
    const tb = eb.traces.get(pair.bId);
    if (!ta || !tb) {
      missingEvidence.push({ aId: pair.aId, bId: pair.bId });
      pairs.push({ aId: pair.aId, bId: pair.bId, aNode, bNode, divergent: false });
      continue;
    }
    const unitError = ta.unitError ?? tb.unitError;
    const dimsA = dimsKey(ta.dims);
    const dimsB = dimsKey(tb.dims);
    const valueDiff = !ta.value.equals(tb.value);
    const dimsDiff = dimsA !== dimsB;
    const divergent = valueDiff || dimsDiff || unitError !== undefined;
    const step: ChainStep = {
      aId: pair.aId,
      bId: pair.bId,
      op: aNode.op,
      aValue: ta.value.toString(),
      bValue: tb.value.toString(),
      delta: tb.value.sub(ta.value).toString(),
      dims: dimsDiff ? `${dimsA} vs ${dimsB}` : dimsA,
    };
    if (unitError) step.unitError = unitError;
    if (divergent) divergentPairIds.add(pair.aId);
    pairs.push({ aId: pair.aId, bId: pair.bId, aNode, bNode, divergent, step });
  }

  // ---- root causes: local rule diffs with no upstream rule diff ----------
  const pairByA = new Map(pairs.map((p) => [p.aId, p]));
  const ruleDiffAt = new Map<string, RuleDiff>();
  for (const p of pairs) {
    const d = localRuleDiff(p.aNode, p.bNode, pa, pb, consumersA);
    if (d) ruleDiffAt.set(p.aId, d);
  }
  const hasRuleDiffAncestor = (aId: string): boolean => {
    const seen = new Set<string>();
    const stack = [...nodeInputs(nodesA.get(aId)!)];
    while (stack.length) {
      const cur = stack.pop()!;
      if (seen.has(cur)) continue;
      seen.add(cur);
      if (ruleDiffAt.has(cur)) return true;
      const n = nodesA.get(cur);
      if (n) stack.push(...nodeInputs(n));
    }
    return false;
  };
  const rootCauses: RuleDiff[] = [];
  for (const [aId, d] of ruleDiffAt) {
    if (!hasRuleDiffAncestor(aId)) rootCauses.push(d);
  }

  // structural root causes: unmatched stage feeding an aligned node
  const matchedA = new Set(alignment.pairs.map((p) => p.aId));
  const matchedB = new Set(alignment.pairs.map((p) => p.bId));
  for (const id of alignment.unmatchedA) {
    const n = nodesA.get(id)!;
    const feedsDivergent = (consumersA.get(id) ?? []).some(
      (c) => matchedA.has(c.id) && divergentPairIds.has(c.id)
    );
    if (feedsDivergent) {
      rootCauses.push({
        kind: "structural",
        aNode: id,
        bNode: null,
        detail: `stage exists only in A: ${describeNode(n)}`,
      });
    }
  }
  const consumersB = new Map<string, Node[]>();
  for (const n of pb.nodes) {
    for (const up of nodeInputs(n)) {
      const arr = consumersB.get(up) ?? [];
      arr.push(n);
      consumersB.set(up, arr);
    }
  }
  const divergentPairIdsB = new Set(
    pairs.filter((p) => p.divergent).map((p) => p.bId)
  );
  for (const id of alignment.unmatchedB) {
    const n = nodesB.get(id)!;
    const feedsDivergent = (consumersB.get(id) ?? []).some(
      (c) => matchedB.has(c.id) && divergentPairIdsB.has(c.id)
    );
    if (feedsDivergent) {
      rootCauses.push({
        kind: "structural",
        aNode: null,
        bNode: id,
        detail: `stage exists only in B: ${describeNode(n)}`,
      });
    }
  }

  // ---- first divergence (earliest in A's topological order) --------------
  const divergentPairs = pairs.filter((p) => p.divergent && p.step);
  const firstDivergence = divergentPairs.length ? divergentPairs[0].step : undefined;

  // ---- divergent outputs ---------------------------------------------------
  const divergentOutputs: CompareReport["divergentOutputs"] = [];
  const outputNames = new Set([...ea.outputs.keys(), ...eb.outputs.keys()]);
  for (const name of outputNames) {
    const oa = ea.outputs.get(name);
    const ob = eb.outputs.get(name);
    if (!oa || !ob) {
      divergentOutputs.push({
        name,
        aValue: oa ? oa.value.toString() : "<missing>",
        bValue: ob ? ob.value.toString() : "<missing>",
        delta: "?",
      });
      continue;
    }
    if (!oa.value.equals(ob.value) || dimsKey(oa.dims) !== dimsKey(ob.dims)) {
      divergentOutputs.push({
        name,
        aValue: oa.value.toString(),
        bValue: ob.value.toString(),
        delta: ob.value.sub(oa.value).toString(),
      });
    }
  }

  // ---- exact difference chains: divergent ancestors of each bad output ---
  const differenceChains: CompareReport["differenceChains"] = [];
  const stepByA = new Map(divergentPairs.map((p) => [p.aId, p.step!]));
  for (const out of divergentOutputs) {
    const outNodeA = pa.nodes.find((n) => n.op === "output" && n.name === out.name);
    if (!outNodeA) continue;
    const steps: ChainStep[] = [];
    const seen = new Set<string>();
    const walk = (id: string): void => {
      if (seen.has(id)) return;
      seen.add(id);
      const st = stepByA.get(id);
      if (st) steps.push(st);
      const n = nodesA.get(id);
      if (n) for (const up of nodeInputs(n)) walk(up);
    };
    walk(outNodeA.id);
    const orderA = new Map(pa.nodes.map((n, i) => [n.id, i]));
    steps.sort((x, y) => (orderA.get(x.aId) ?? 0) - (orderA.get(y.aId) ?? 0));
    differenceChains.push({ output: out.name, steps });
  }

  // ---- status --------------------------------------------------------------
  const notes: string[] = [];
  let status: CompareReport["status"];
  const divergentIntermediates = divergentPairs.filter((p) => p.aNode.op !== "output");
  if (divergentOutputs.length > 0) {
    if (divergentIntermediates.length === 0 && missingEvidence.length > 0) {
      status = "cannot-locate";
      notes.push(
        "outputs differ but no divergent aligned intermediate has retained " +
          "evidence; missing intermediate evidence at: " +
          missingEvidence.map((m) => `${m.aId}~${m.bId}`).join(", ")
      );
    } else {
      status = "output-divergence";
    }
  } else if (divergentPairs.length > 0) {
    status = "intermediate-divergence";
    notes.push("final outputs are exactly equal, but aligned intermediates differ");
  } else {
    status = "identical";
  }
  if (missingEvidence.length > 0 && status !== "cannot-locate") {
    notes.push(
      `warning: ${missingEvidence.length} aligned pair(s) lack retained intermediate evidence`
    );
  }

  return {
    status,
    inputs: inputStrings,
    alignedPairs: alignment.pairs.length,
    unmatchedA: alignment.unmatchedA,
    unmatchedB: alignment.unmatchedB,
    firstDivergence,
    rootCauses,
    differenceChains,
    divergentOutputs,
    missingEvidence,
    notes,
  };
}

function describeNode(n: Node): string {
  switch (n.op) {
    case "const":
      return `const ${n.id} = ${n.value}${n.unit ? " " + n.unit : ""}`;
    case "input":
      return `input ${n.id} <- ${n.slot}`;
    case "round":
      return `round ${n.id}(places=${n.places}, mode=${n.mode})`;
    case "convert":
      return `convert ${n.id} -> ${n.unit}`;
    case "output":
      return `output ${n.name}`;
    case "neg":
      return `neg ${n.id}`;
    default:
      return `${n.op} ${n.id}`;
  }
}
