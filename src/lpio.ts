/**
 * JSON (de)serialization for LPs and results. Numbers are represented as
 * strings: "3", "-2", "3/4".
 */
import { Frac } from "./fraction";
import { LP, SolveResult, Status } from "./simplex";

export interface LPJson {
  c: string[];
  A: string[][];
  b: string[];
  budget?: number;
}

export function parseLP(json: unknown): LP {
  if (typeof json !== "object" || json === null)
    throw new Error("problem must be a JSON object");
  const o = json as Record<string, unknown>;
  if (!Array.isArray(o.c) || !Array.isArray(o.A) || !Array.isArray(o.b))
    throw new Error("problem needs arrays c, A, b");
  const c = (o.c as unknown[]).map((v) => Frac.parse(String(v)));
  const A = (o.A as unknown[]).map((row, i) => {
    if (!Array.isArray(row)) throw new Error(`A[${i}] is not an array`);
    const r = (row as unknown[]).map((v) => Frac.parse(String(v)));
    if (r.length !== c.length)
      throw new Error(`A[${i}] has ${r.length} entries, c has ${c.length}`);
    return r;
  });
  const b = (o.b as unknown[]).map((v) => Frac.parse(String(v)));
  if (b.length !== A.length)
    throw new Error(`b has ${b.length} entries, A has ${A.length} rows`);
  return { c, A, b };
}

export function resultToJson(res: SolveResult): Record<string, unknown> {
  const out: Record<string, unknown> = {
    status: res.status,
    pivots: res.pivots,
  };
  if (res.value !== undefined) out.value = res.value.toString();
  if (res.x !== undefined) out.x = res.x.map((f) => f.toString());
  if (res.y !== undefined) out.y = res.y.map((f) => f.toString());
  if (res.d !== undefined) out.d = res.d.map((f) => f.toString());
  return out;
}

export function jsonToResult(json: unknown): SolveResult {
  if (typeof json !== "object" || json === null)
    throw new Error("result must be a JSON object");
  const o = json as Record<string, unknown>;
  const status = String(o.status) as Status;
  if (!["optimal", "infeasible", "unbounded", "budget_exhausted"].includes(status))
    throw new Error(`unknown status: ${status}`);
  const vec = (v: unknown): Frac[] | undefined =>
    v === undefined ? undefined : (v as unknown[]).map((x) => Frac.parse(String(x)));
  return {
    status,
    pivots: typeof o.pivots === "number" ? o.pivots : 0,
    value: o.value === undefined ? undefined : Frac.parse(String(o.value)),
    x: vec(o.x),
    y: vec(o.y),
    d: vec(o.d),
  };
}
