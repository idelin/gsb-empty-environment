/**
 * Pretty-printer: renders an AST back to formatted source code.
 *
 * Uses a Wadler-style document algebra:
 *  - `text`     literal output
 *  - `concat`   sequence of docs
 *  - `line`     a space when the enclosing group fits, otherwise a newline
 *  - `softline` empty when the enclosing group fits, otherwise a newline
 *  - `group`    try to render flat; break all its lines if it does not fit
 *  - `indent`   increase the indentation level for nested newlines
 *
 * Breaks only ever happen at `line`/`softline` positions, which the
 * expression printer places exclusively after operators and commas, so
 * wrapping always occurs at legal syntactic boundaries.
 */

import type { Expr } from "./ast.ts";
import { BIN_PREC, UNARY_PREC } from "./parser.ts";

export type Doc =
  | { type: "text"; s: string }
  | { type: "concat"; parts: Doc[] }
  | { type: "line" }
  | { type: "softline" }
  | { type: "group"; doc: Doc }
  | { type: "indent"; doc: Doc };

const text = (s: string): Doc => ({ type: "text", s });
const concat = (...parts: Doc[]): Doc => ({ type: "concat", parts });
const line: Doc = { type: "line" };
const softline: Doc = { type: "softline" };
const group = (doc: Doc): Doc => ({ type: "group", doc });
const indent = (doc: Doc): Doc => ({ type: "indent", doc });

const INDENT_WIDTH = 2;
const CALL_PREC = 8;
const ATOM_PREC = 9;

function join(sep: Doc, docs: Doc[]): Doc {
  const parts: Doc[] = [];
  docs.forEach((d, i) => {
    if (i > 0) parts.push(sep);
    parts.push(d);
  });
  return concat(...parts);
}

function precOf(e: Expr): number {
  switch (e.kind) {
    case "binary":
      return BIN_PREC[e.op];
    case "unary":
      return UNARY_PREC;
    case "call":
      return CALL_PREC;
    default:
      return ATOM_PREC;
  }
}

/**
 * Decides whether `e` needs parentheses in its syntactic context.
 * Parentheses are emitted conservatively so that re-parsing the output
 * always yields a structurally identical AST:
 *  - lower precedence than the context always needs parens;
 *  - equal precedence on the right-hand side (or as a unary operand)
 *    always needs parens, because the parser is left-associative.
 */
function needsParens(e: Expr, ctxPrec: number, isRightSide: boolean): boolean {
  const p = precOf(e);
  if (p < ctxPrec) return true;
  if (p === ctxPrec && isRightSide && (e.kind === "binary" || e.kind === "unary")) {
    return true;
  }
  return false;
}

function exprDoc(e: Expr, ctxPrec: number, isRightSide: boolean): Doc {
  let doc: Doc;
  switch (e.kind) {
    case "num":
      doc = text(formatNumber(e.value));
      break;
    case "str":
      doc = text(JSON.stringify(e.value));
      break;
    case "bool":
      doc = text(e.value ? "true" : "false");
      break;
    case "ident":
      doc = text(e.name);
      break;
    case "unary": {
      const operand = exprDoc(e.operand, UNARY_PREC, true);
      doc = concat(text(e.op), operand);
      break;
    }
    case "binary": {
      const prec = BIN_PREC[e.op];
      const left = exprDoc(e.left, prec, false);
      const right = exprDoc(e.right, prec, true);
      // Flat:  `left op right`
      // Broken: `left\n  + right` (operator leads the continuation line)
      doc = group(concat(left, indent(concat(line, text(e.op), text(" "), right))));
      break;
    }
    case "call": {
      const callee = exprDoc(e.callee, CALL_PREC, false);
      if (e.args.length === 0) {
        doc = concat(callee, text("()"));
        break;
      }
      const args = e.args.map((a) => exprDoc(a, 0, false));
      // Flat:  `f(a, b)`
      // Broken: `f(\n  a,\n  b\n)`
      doc = concat(
        callee,
        text("("),
        group(concat(indent(concat(softline, join(concat(text(","), line), args))), softline)),
        text(")"),
      );
      break;
    }
  }
  return needsParens(e, ctxPrec, isRightSide) ? concat(text("("), doc, text(")")) : doc;
}

function formatNumber(value: number): string {
  if (!Number.isFinite(value)) {
    throw new Error(`Cannot print non-finite number literal: ${value}`);
  }
  if (Object.is(value, -0)) {
    throw new Error("Cannot print -0 as a number literal");
  }
  if (value < 0) {
    throw new Error(
      `Negative number literal ${value} is not representable; use unary minus instead`,
    );
  }
  return String(value);
}

/** A pending command: (indent level, flat mode, doc). */
type Cmd = [indentLevel: number, flat: boolean, doc: Doc];

/**
 * Whether the pending commands fit within `width` more columns.
 * `cmds` is a stack (processed from the end) that includes everything
 * remaining on the current output line, so trailing tokens such as `)`
 * are accounted for. A `line`/`softline` in break mode ends the check
 * (the output line ends there); in flat mode it counts as a space.
 */
function fits(width: number, cmds: Cmd[]): boolean {
  let remaining = width;
  const stack = [...cmds];
  while (remaining >= 0 && stack.length > 0) {
    const [ind, flat, doc] = stack.pop()!;
    switch (doc.type) {
      case "text":
        remaining -= doc.s.length;
        break;
      case "concat":
        for (let i = doc.parts.length - 1; i >= 0; i--) {
          stack.push([ind, flat, doc.parts[i]]);
        }
        break;
      case "line":
        if (!flat) return true;
        remaining -= 1;
        break;
      case "softline":
        if (!flat) return true;
        break;
      case "group":
        stack.push([ind, true, doc.doc]);
        break;
      case "indent":
        stack.push([ind + INDENT_WIDTH, flat, doc.doc]);
        break;
    }
  }
  return remaining >= 0;
}

function render(doc: Doc, width: number): string {
  let out = "";
  let col = 0;
  const stack: Cmd[] = [[0, false, doc]];

  while (stack.length > 0) {
    const [ind, flat, d] = stack.pop()!;
    switch (d.type) {
      case "text":
        out += d.s;
        col += d.s.length;
        break;
      case "concat":
        for (let i = d.parts.length - 1; i >= 0; i--) {
          stack.push([ind, flat, d.parts[i]]);
        }
        break;
      case "line":
        if (flat) {
          out += " ";
          col += 1;
        } else {
          out += "\n" + " ".repeat(ind);
          col = ind;
        }
        break;
      case "softline":
        if (!flat) {
          out += "\n" + " ".repeat(ind);
          col = ind;
        }
        break;
      case "group": {
        // The rest of the stack is included so that whatever follows the
        // group on the same output line counts towards the width check.
        const fitsFlat = flat || fits(width - col, [...stack, [ind, true, d.doc]]);
        stack.push([ind, fitsFlat, d.doc]);
        break;
      }
      case "indent":
        stack.push([ind + INDENT_WIDTH, flat, d.doc]);
        break;
    }
  }
  return out;
}

export const DEFAULT_WIDTH = 80;

/** Render an expression as formatted source code within `width` columns. */
export function prettyPrint(expr: Expr, width: number = DEFAULT_WIDTH): string {
  return render(exprDoc(expr, 0, false), width);
}
