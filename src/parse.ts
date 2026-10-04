/**
 * JSON 输入/输出的解析与格式化。
 * 系数接受整数（JSON number 或 "3"）或分数字符串（"22/7"、"-3/4"）。
 */

import type { Rat } from "./rational.ts";
import { parseRat, rat, formatRat } from "./rational.ts";
import type { LP, SolveResult } from "./simplex.ts";

export interface ProblemInput extends LP {
  budget?: number;
}

export function parseCoefficient(value: unknown): Rat {
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) {
      throw new Error(`coefficient must be an integer or "p/q" string, got ${value}`);
    }
    return rat(BigInt(value));
  }
  if (typeof value === "string") return parseRat(value);
  throw new Error(`coefficient must be an integer or "p/q" string, got ${JSON.stringify(value)}`);
}

function parseVector(value: unknown, name: string): Rat[] {
  if (!Array.isArray(value)) throw new Error(`${name} must be an array`);
  return value.map(parseCoefficient);
}

export function parseProblem(raw: unknown): ProblemInput {
  if (typeof raw !== "object" || raw === null) throw new Error("problem must be a JSON object");
  const obj = raw as Record<string, unknown>;
  const c = parseVector(obj.c, "c");
  const b = parseVector(obj.b, "b");
  if (!Array.isArray(obj.A)) throw new Error("A must be an array of arrays");
  const A = obj.A.map((row, i) => parseVector(row, `A[${i}]`));
  if (A.length !== b.length) {
    throw new Error(`A has ${A.length} rows but b has ${b.length} entries`);
  }
  A.forEach((row, i) => {
    if (row.length !== c.length) {
      throw new Error(`A[${i}] has ${row.length} entries but c has ${c.length}`);
    }
  });
  let budget: number | undefined;
  if (obj.budget !== undefined) {
    if (typeof obj.budget !== "number" || !Number.isInteger(obj.budget) || obj.budget < 0) {
      throw new Error("budget must be a non-negative integer");
    }
    budget = obj.budget;
  }
  return { c, A, b, budget };
}

function parseOptVector(value: unknown, name: string): Rat[] | undefined {
  if (value === undefined) return undefined;
  return parseVector(value, name);
}

/** 把 CLI/JSON 形式的结果（分数字符串）解析回 SolveResult，供检查器使用。 */
export function parseResult(raw: unknown): SolveResult {
  if (typeof raw !== "object" || raw === null) throw new Error("result must be a JSON object");
  const obj = raw as Record<string, unknown>;
  const status = obj.status;
  if (
    status !== "optimal" &&
    status !== "infeasible" &&
    status !== "unbounded" &&
    status !== "iteration_limit"
  ) {
    throw new Error(`unknown status: ${JSON.stringify(status)}`);
  }
  return {
    status,
    iterations: typeof obj.iterations === "number" ? obj.iterations : 0,
    objective: obj.objective !== undefined ? parseCoefficient(obj.objective) : undefined,
    x: parseOptVector(obj.x, "x"),
    y: parseOptVector(obj.y, "y"),
    direction: parseOptVector(obj.direction, "direction"),
  };
}

export interface FormattedResult {
  status: string;
  iterations: number;
  objective?: string;
  x?: string[];
  y?: string[];
  direction?: string[];
  hasAlternativeOptima?: boolean;
}

/** 输出全部为约分分数的字符串形式。 */
export function formatResult(result: SolveResult): FormattedResult {
  const out: FormattedResult = { status: result.status, iterations: result.iterations };
  if (result.objective !== undefined) out.objective = formatRat(result.objective);
  if (result.x !== undefined) out.x = result.x.map(formatRat);
  if (result.y !== undefined) out.y = result.y.map(formatRat);
  if (result.direction !== undefined) out.direction = result.direction.map(formatRat);
  if (result.hasAlternativeOptima !== undefined) {
    out.hasAlternativeOptima = result.hasAlternativeOptima;
  }
  return out;
}
