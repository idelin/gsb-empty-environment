export type Expr =
  | { kind: "num"; value: number; raw: string }
  | { kind: "var"; name: string }
  | { kind: "add" | "mul" | "div"; left: Expr; right: Expr }
  | { kind: "pow"; base: Expr; exponent: number };

const OP_SYMBOL = { add: "+", mul: "*", div: "/" } as const;

const PREC_ADD = 1;
const PREC_MUL = 2;
const PREC_POW = 3;

export function formatExpr(expr: Expr): string {
  return fmt(expr, 0);
}

function fmt(expr: Expr, parentPrec: number): string {
  switch (expr.kind) {
    case "num":
      return expr.raw;
    case "var":
      return expr.name;
    case "add":
    case "mul":
    case "div": {
      const prec = expr.kind === "add" ? PREC_ADD : PREC_MUL;
      const text = `${fmt(expr.left, prec)} ${OP_SYMBOL[expr.kind]} ${fmt(expr.right, prec + 1)}`;
      return prec < parentPrec ? `(${text})` : text;
    }
    case "pow": {
      const exponent =
        expr.exponent < 0 ? `(${expr.exponent})` : String(expr.exponent);
      const text = `${fmt(expr.base, PREC_POW)}^${exponent}`;
      return PREC_POW < parentPrec ? `(${text})` : text;
    }
  }
}
