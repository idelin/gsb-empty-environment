// 语言定义:8 位有符号整数小语言(赋值 / 条件 / 有界循环 / 断言)。
// 语义约定:
//   - 所有算术按 int8 回绕(-128..127),同时可检测溢出;
//   - 条件以“非零为真”;逻辑运算符不短路(两侧都求值);
//   - 变量未赋值时默认为 0;循环带有界 unroll 上界,超界路径被截断。

export const INT8_MIN = -128;
export const INT8_MAX = 127;

export function toInt8(x: number): number {
  const v = ((Math.trunc(x) % 256) + 256) % 256;
  return v > INT8_MAX ? v - 256 : v;
}

export type ArithOp = "+" | "-" | "*";
export type CmpOp = "<" | "<=" | "==" | "!=" | ">=" | ">";
export type LogicOp = "&&" | "||";

export type Expr =
  | { kind: "const"; value: number }
  | { kind: "input"; index: number }
  | { kind: "var"; name: string }
  | { kind: "neg"; e: Expr }
  | { kind: "not"; e: Expr }
  | { kind: "bin"; op: ArithOp; l: Expr; r: Expr }
  | { kind: "cmp"; op: CmpOp; l: Expr; r: Expr }
  | { kind: "logic"; op: LogicOp; l: Expr; r: Expr }
  // 仅出现在路径条件中:“op(l, r) 发生溢出”(op 为 "neg" 时为一元,r 为 null)
  | { kind: "ovf"; op: ArithOp | "neg"; l: Expr; r: Expr | null };

export type Stmt =
  | { kind: "assign"; name: string; expr: Expr }
  | { kind: "if"; id: number; cond: Expr; then: Stmt[]; els: Stmt[] }
  | { kind: "while"; id: number; cond: Expr; body: Stmt[]; bound: number }
  | { kind: "assert"; id: number; cond: Expr };

export type Program = {
  name: string;
  inputs: number;
  targetAssert: number;
  body: Stmt[];
};

// ---------- 构造器(带常量折叠,兼作符号表达式的化简) ----------

export const C = (value: number): Expr => ({ kind: "const", value: toInt8(value) });
export const inp = (index: number): Expr => ({ kind: "input", index });
export const V = (name: string): Expr => ({ kind: "var", name });

const isConst = (e: Expr): e is { kind: "const"; value: number } => e.kind === "const";

export function arithRaw(op: ArithOp, a: number, b: number): number {
  switch (op) {
    case "+": return a + b;
    case "-": return a - b;
    case "*": return a * b;
  }
}

export function arithOverflows(op: ArithOp | "neg", a: number, b = 0): boolean {
  const raw = op === "neg" ? -a : arithRaw(op, a, b);
  return raw !== toInt8(raw);
}

export function applyCmp(op: CmpOp, a: number, b: number): boolean {
  switch (op) {
    case "<": return a < b;
    case "<=": return a <= b;
    case "==": return a === b;
    case "!=": return a !== b;
    case ">=": return a >= b;
    case ">": return a > b;
  }
}

export function neg(e: Expr): Expr {
  if (isConst(e)) return C(-e.value);
  return { kind: "neg", e };
}

export function not(e: Expr): Expr {
  if (isConst(e)) return C(e.value === 0 ? 1 : 0);
  return { kind: "not", e };
}

export function bin(op: ArithOp, l: Expr, r: Expr): Expr {
  if (isConst(l) && isConst(r)) return C(arithRaw(op, l.value, r.value));
  return { kind: "bin", op, l, r };
}

export function cmp(op: CmpOp, l: Expr, r: Expr): Expr {
  if (isConst(l) && isConst(r)) return C(applyCmp(op, l.value, r.value) ? 1 : 0);
  return { kind: "cmp", op, l, r };
}

export function and(l: Expr, r: Expr): Expr {
  if (isConst(l)) return l.value !== 0 ? r : C(0);
  if (isConst(r)) return r.value !== 0 ? l : C(0);
  return { kind: "logic", op: "&&", l, r };
}

export function or(l: Expr, r: Expr): Expr {
  if (isConst(l)) return l.value !== 0 ? C(1) : r;
  if (isConst(r)) return r.value !== 0 ? C(1) : l;
  return { kind: "logic", op: "||", l, r };
}

export const add = (l: Expr, r: Expr): Expr => bin("+", l, r);
export const sub = (l: Expr, r: Expr): Expr => bin("-", l, r);
export const mul = (l: Expr, r: Expr): Expr => bin("*", l, r);
export const lt = (l: Expr, r: Expr): Expr => cmp("<", l, r);
export const le = (l: Expr, r: Expr): Expr => cmp("<=", l, r);
export const eq = (l: Expr, r: Expr): Expr => cmp("==", l, r);
export const ne = (l: Expr, r: Expr): Expr => cmp("!=", l, r);
export const ge = (l: Expr, r: Expr): Expr => cmp(">=", l, r);
export const gt = (l: Expr, r: Expr): Expr => cmp(">", l, r);

// 把任意表达式归一化为布尔条件(非零为真)
export function truthy(e: Expr): Expr {
  switch (e.kind) {
    case "const": return C(e.value !== 0 ? 1 : 0);
    case "cmp":
    case "logic":
    case "not":
    case "ovf":
      return e;
    default:
      return ne(e, C(0));
  }
}

// ---------- 具体执行 ----------

export type ConcreteOutcome = {
  status: "ok" | "assert-failed" | "bound-exceeded";
  failedAssert: number | null;
  overflow: boolean;
  edges: Set<string>;
};

export function runConcrete(prog: Program, inputs: number[]): ConcreteOutcome {
  const store = new Map<string, number>();
  const edges = new Set<string>();
  let overflow = false;

  function evalE(e: Expr): number {
    switch (e.kind) {
      case "const": return e.value;
      case "input": return toInt8(inputs[e.index] ?? 0);
      case "var": return store.get(e.name) ?? 0;
      case "neg": {
        const v = evalE(e.e);
        if (arithOverflows("neg", v)) overflow = true;
        return toInt8(-v);
      }
      case "not": return evalE(e.e) === 0 ? 1 : 0;
      case "bin": {
        const l = evalE(e.l);
        const r = evalE(e.r);
        if (arithOverflows(e.op, l, r)) overflow = true;
        return toInt8(arithRaw(e.op, l, r));
      }
      case "cmp": return applyCmp(e.op, evalE(e.l), evalE(e.r)) ? 1 : 0;
      case "logic": {
        const l = evalE(e.l) !== 0;
        const r = evalE(e.r) !== 0;
        return (e.op === "&&" ? l && r : l || r) ? 1 : 0;
      }
      case "ovf": throw new Error("ovf 节点只允许出现在路径条件中");
    }
  }

  function finish(status: ConcreteOutcome["status"], failedAssert: number | null): ConcreteOutcome {
    return { status, failedAssert, overflow, edges };
  }

  // 返回 null 表示继续顺序执行,否则表示运行终止(断言失败 / 循环超界)
  function execBlock(stmts: Stmt[]): ConcreteOutcome | null {
    for (const s of stmts) {
      switch (s.kind) {
        case "assign":
          store.set(s.name, evalE(s.expr));
          break;
        case "if": {
          const c = evalE(s.cond) !== 0;
          edges.add(`if#${s.id}:${c ? "then" : "else"}`);
          const r = execBlock(c ? s.then : s.els);
          if (r) return r;
          break;
        }
        case "while": {
          let i = 0;
          for (;;) {
            if (evalE(s.cond) === 0) {
              edges.add(`while#${s.id}:exit`);
              break;
            }
            if (i >= s.bound) {
              edges.add(`while#${s.id}:bound`);
              return finish("bound-exceeded", null);
            }
            edges.add(`while#${s.id}:enter`);
            const r = execBlock(s.body);
            if (r) return r;
            i++;
          }
          break;
        }
        case "assert": {
          const ok = evalE(s.cond) !== 0;
          edges.add(`assert#${s.id}:${ok ? "pass" : "fail"}`);
          if (!ok) return finish("assert-failed", s.id);
          break;
        }
      }
    }
    return null;
  }

  return execBlock(prog.body) ?? finish("ok", null);
}

// ---------- 静态信息 ----------

export function collectEdges(prog: Program): string[] {
  const edges: string[] = [];
  const walk = (stmts: Stmt[]): void => {
    for (const s of stmts) {
      if (s.kind === "if") {
        edges.push(`if#${s.id}:then`, `if#${s.id}:else`);
        walk(s.then);
        walk(s.els);
      } else if (s.kind === "while") {
        edges.push(`while#${s.id}:enter`, `while#${s.id}:exit`, `while#${s.id}:bound`);
        walk(s.body);
      } else if (s.kind === "assert") {
        edges.push(`assert#${s.id}:pass`, `assert#${s.id}:fail`);
      }
    }
  };
  walk(prog.body);
  return edges;
}

export function collectAssertIds(prog: Program): number[] {
  const ids: number[] = [];
  const walk = (stmts: Stmt[]): void => {
    for (const s of stmts) {
      if (s.kind === "assert") ids.push(s.id);
      else if (s.kind === "if") { walk(s.then); walk(s.els); }
      else if (s.kind === "while") walk(s.body);
    }
  };
  walk(prog.body);
  return ids;
}
