/**
 * Pipeline = DAG of exact operations. Evaluation keeps every intermediate
 * as an exact Rational plus a dimension vector, so two pipelines can be
 * compared value-by-value without any floating point anywhere.
 */
import { Rational } from "./rational.ts";
import { roundRational, type RoundingMode } from "./rounding.ts";
import {
  dimsEqual,
  dimsInv,
  dimsKey,
  dimsMul,
  fromBase,
  lookupUnit,
  toBase,
  SI_TABLE,
  type Dims,
  type UnitTable,
} from "./units.ts";

export type Node =
  | { id: string; op: "const"; value: string; unit?: string; label?: string }
  | { id: string; op: "input"; slot: string; unit?: string }
  | { id: string; op: "add" | "sub" | "mul" | "div"; a: string; b: string }
  | { id: string; op: "neg"; a: string }
  | { id: string; op: "round"; a: string; places: number; mode: RoundingMode; unit?: string }
  | { id: string; op: "convert"; a: string; unit: string }
  | { id: string; op: "output"; a: string; name: string };

export interface Pipeline {
  name: string;
  nodes: Node[];
  /** Pipelines may carry their own conversion conventions. */
  unitTable?: UnitTable;
}

export interface NodeTrace {
  value: Rational; // exact, in base units
  dims: Dims;
  unitError?: string; // e.g. "add: m^1 vs s^1"
  arithError?: string; // e.g. "division by zero"
  expressedUnit?: string; // set by convert / round-with-unit
  expressedValue?: Rational; // magnitude in expressedUnit
}

export interface EvalResult {
  pipeline: string;
  /** Evidence actually retained (may be a subset when keepTrace filters). */
  traces: Map<string, NodeTrace>;
  outputs: Map<string, NodeTrace>; // output name -> trace (always retained)
  topoOrder: string[];
}

export interface EvalOptions {
  /**
   * Evidence filter: nodes for which it returns false are evaluated (their
   * values feed downstream) but their trace is NOT retained as evidence.
   * Models an external pipeline that only exposes some intermediates.
   */
  keepTrace?: (nodeId: string) => boolean;
}

export function evaluate(
  p: Pipeline,
  inputs: Record<string, string | Rational> = {},
  opts: EvalOptions = {}
): EvalResult {
  const table = p.unitTable ?? SI_TABLE;
  const full = new Map<string, NodeTrace>();
  const byId = new Map<string, Node>();
  for (const n of p.nodes) {
    if (byId.has(n.id)) throw new Error(`duplicate node id ${n.id}`);
    byId.set(n.id, n);
  }
  const topoOrder: string[] = [];

  const need = (id: string): NodeTrace => {
    const t = full.get(id);
    if (!t) throw new Error(`node ${id} referenced before definition`);
    return t;
  };

  for (const n of p.nodes) {
    let trace: NodeTrace;
    switch (n.op) {
      case "const": {
        const b = toBase(table, Rational.parse(n.value), n.unit ?? "1");
        trace = { value: b.value, dims: b.dims };
        break;
      }
      case "input": {
        const raw = inputs[n.slot];
        if (raw === undefined) throw new Error(`missing input slot ${n.slot}`);
        const mag = typeof raw === "string" ? Rational.parse(raw) : raw;
        const b = toBase(table, mag, n.unit ?? "1");
        trace = { value: b.value, dims: b.dims };
        break;
      }
      case "add":
      case "sub": {
        const x = need(n.a);
        const y = need(n.b);
        const val = n.op === "add" ? x.value.add(y.value) : x.value.sub(y.value);
        trace = { value: val, dims: x.dims };
        if (!dimsEqual(x.dims, y.dims)) {
          trace.unitError = `${n.op}: dimension mismatch ${dimsKey(x.dims)} vs ${dimsKey(y.dims)}`;
        }
        break;
      }
      case "mul": {
        const x = need(n.a);
        const y = need(n.b);
        trace = { value: x.value.mul(y.value), dims: dimsMul(x.dims, y.dims) };
        break;
      }
      case "div": {
        const x = need(n.a);
        const y = need(n.b);
        if (y.value.isZero()) {
          trace = {
            value: Rational.zero(),
            dims: dimsMul(x.dims, dimsInv(y.dims)),
            arithError: "division by zero",
          };
        } else {
          trace = { value: x.value.div(y.value), dims: dimsMul(x.dims, dimsInv(y.dims)) };
        }
        break;
      }
      case "neg": {
        const x = need(n.a);
        trace = { value: x.value.neg(), dims: x.dims };
        break;
      }
      case "round": {
        const x = need(n.a);
        let mag = x.value;
        let expressedUnit: string | undefined;
        if (n.unit !== undefined) {
          const def = lookupUnit(table, n.unit);
          if (!dimsEqual(def.dims, x.dims)) {
            trace = {
              value: x.value,
              dims: x.dims,
              unitError: `round: cannot express ${dimsKey(x.dims)} in ${n.unit}`,
            };
            break;
          }
          mag = fromBase(table, x.value, n.unit);
          expressedUnit = n.unit;
        }
        const rounded = roundRational(mag, { places: n.places, mode: n.mode });
        trace = {
          value: expressedUnit ? toBase(table, rounded, expressedUnit).value : rounded,
          dims: x.dims,
        };
        if (expressedUnit) {
          trace.expressedUnit = expressedUnit;
          trace.expressedValue = rounded;
        }
        break;
      }
      case "convert": {
        const x = need(n.a);
        const def = lookupUnit(table, n.unit);
        if (!dimsEqual(def.dims, x.dims)) {
          trace = {
            value: x.value,
            dims: x.dims,
            unitError: `convert: cannot convert ${dimsKey(x.dims)} to ${n.unit} (${dimsKey(def.dims)})`,
          };
        } else {
          trace = {
            value: x.value, // base value unchanged; conversion is exact
            dims: x.dims,
            expressedUnit: n.unit,
            expressedValue: fromBase(table, x.value, n.unit),
          };
        }
        break;
      }
      case "output": {
        const x = need(n.a);
        trace = { ...x };
        break;
      }
    }
    full.set(n.id, trace);
    topoOrder.push(n.id);
  }

  const traces = new Map<string, NodeTrace>();
  const outputs = new Map<string, NodeTrace>();
  for (const n of p.nodes) {
    const t = full.get(n.id)!;
    if (n.op === "output") outputs.set(n.name, t);
    if (n.op === "output" || !opts.keepTrace || opts.keepTrace(n.id)) {
      traces.set(n.id, t);
    }
  }
  return { pipeline: p.name, traces, outputs, topoOrder };
}
