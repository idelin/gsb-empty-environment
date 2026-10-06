/**
 * Shared language definition: AST, values, environments, errors and
 * error-report formatting used by both interpreters.
 */

export type Value = number | boolean;

export type Expr =
  | { tag: "num"; value: number }
  | { tag: "bool"; value: boolean }
  | { tag: "var"; name: string }
  | { tag: "prim"; op: PrimOp; args: Expr[] }
  | { tag: "if"; cond: Expr; then: Expr; else: Expr }
  | { tag: "let"; name: string; value: Expr; body: Expr }
  | { tag: "begin"; exprs: Expr[] }
  | { tag: "call"; name: string; args: Expr[] }
  | { tag: "raise"; message: string };

export type PrimOp =
  | "+" | "-" | "*" | "/" | "%"
  | "=" | "<" | "<=" | ">" | ">="
  | "and" | "or" | "not";

export interface FunDef {
  name: string;
  params: string[];
  body: Expr;
}

export interface Program {
  functions: FunDef[];
  main: Expr;
}

/** One observed call: function name plus the argument values it received. */
export interface FrameInfo {
  funName: string;
  args: Value[];
}

/**
 * Snapshot of interpreter state attached to an error at the moment it is
 * raised. `nonTailStack` is always complete (oldest first). `recentTailCalls`
 * is the bounded summary of the most recent tail calls (oldest of the kept
 * entries first); `tailElided` says how many older tail calls were dropped.
 */
export interface ErrorReport {
  message: string;
  nonTailStack: FrameInfo[];
  recentTailCalls: FrameInfo[];
  tailElided: number;
  tailTotal: number;
}

export class InterpError extends Error {
  snapshot?: ErrorReport;
  constructor(message: string) {
    super(message);
    this.name = "InterpError";
  }
}

export class Env {
  private vars = new Map<string, Value>();
  constructor(private parent: Env | null = null) {}
  define(name: string, value: Value): void {
    this.vars.set(name, value);
  }
  lookup(name: string): Value {
    const hit = this.vars.get(name);
    if (hit !== undefined) return hit;
    if (this.parent) return this.parent.lookup(name);
    throw new InterpError(`undefined variable: ${name}`);
  }
}

export function formatValue(v: Value): string {
  return typeof v === "boolean" ? (v ? "true" : "false") : String(v);
}

export function formatFrame(f: FrameInfo): string {
  return `${f.funName}(${f.args.map(formatValue).join(", ")})`;
}

export function formatReport(r: ErrorReport): string {
  const lines: string[] = [`Error: ${r.message}`];
  lines.push(`Non-tail stack (complete, ${r.nonTailStack.length} frame(s), oldest first):`);
  r.nonTailStack.forEach((f, i) => lines.push(`  #${i} ${formatFrame(f)}`));
  lines.push(
    `Recent tail calls (last ${r.recentTailCalls.length} of ${r.tailTotal}, ` +
      `${r.tailElided} elided):`
  );
  r.recentTailCalls.forEach((f) => lines.push(`  ~ ${formatFrame(f)}`));
  return lines.join("\n");
}

const PRIM_ARITY: Record<PrimOp, number> = {
  "+": 2, "-": 2, "*": 2, "/": 2, "%": 2,
  "=": 2, "<": 2, "<=": 2, ">": 2, ">=": 2,
  "and": 2, "or": 2, "not": 1,
};

function needNumber(op: PrimOp, v: Value): number {
  if (typeof v !== "number") {
    throw new InterpError(`prim ${op}: expected number, got ${formatValue(v)}`);
  }
  return v;
}

function needBoolean(op: PrimOp, v: Value): boolean {
  if (typeof v !== "boolean") {
    throw new InterpError(`prim ${op}: expected boolean, got ${formatValue(v)}`);
  }
  return v;
}

/** Single source of truth for primitive operations (shared by both interpreters). */
export function applyPrim(op: PrimOp, args: Value[]): Value {
  if (args.length !== PRIM_ARITY[op]) {
    throw new InterpError(`prim ${op}: expected ${PRIM_ARITY[op]} args, got ${args.length}`);
  }
  switch (op) {
    case "+": return needNumber(op, args[0]) + needNumber(op, args[1]);
    case "-": return needNumber(op, args[0]) - needNumber(op, args[1]);
    case "*": return needNumber(op, args[0]) * needNumber(op, args[1]);
    case "/": {
      const d = needNumber(op, args[1]);
      if (d === 0) throw new InterpError("division by zero");
      return Math.trunc(needNumber(op, args[0]) / d);
    }
    case "%": {
      const d = needNumber(op, args[1]);
      if (d === 0) throw new InterpError("division by zero");
      return needNumber(op, args[0]) % d;
    }
    case "=": return needNumber(op, args[0]) === needNumber(op, args[1]);
    case "<": return needNumber(op, args[0]) < needNumber(op, args[1]);
    case "<=": return needNumber(op, args[0]) <= needNumber(op, args[1]);
    case ">": return needNumber(op, args[0]) > needNumber(op, args[1]);
    case ">=": return needNumber(op, args[0]) >= needNumber(op, args[1]);
    case "and": return needBoolean(op, args[0]) && needBoolean(op, args[1]);
    case "or": return needBoolean(op, args[0]) || needBoolean(op, args[1]);
    case "not": return !needBoolean(op, args[0]);
  }
}

export function indexFunctions(program: Program): Map<string, FunDef> {
  const table = new Map<string, FunDef>();
  for (const fn of program.functions) {
    if (table.has(fn.name)) throw new InterpError(`duplicate function: ${fn.name}`);
    table.set(fn.name, fn);
  }
  return table;
}
