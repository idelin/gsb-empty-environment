// Pretty printer: Expr AST -> Doc -> formatted source text.
// Breaks overlong lines at legal operator / comma joints, keeps indentation,
// and inserts exactly the parentheses needed for the output to re-parse to
// the identical AST.

import { BINARY_PREC, POSTFIX_PREC, TERNARY_PREC, UNARY_PREC } from './ast.ts';
import type { Expr } from './ast.ts';
import { concat, group, indent, join, line, render, softline, text } from './doc.ts';
import type { Doc } from './doc.ts';

const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const WORD_UNARY = new Set(['typeof', 'void']);

export function prettyPrint(expr: Expr, width = 80): string {
  return render(group(printExpr(expr, 0)), width);
}

function parenIf(doc: Doc, need: boolean): Doc {
  return need ? concat([text('('), doc, text(')')]) : doc;
}

function escapeString(s: string): string {
  return s.replace(/["\\\n\r\t]/g, (c) => {
    if (c === '"') return '\\"';
    if (c === '\\') return '\\\\';
    if (c === '\n') return '\\n';
    if (c === '\r') return '\\r';
    return '\\t';
  });
}

function printKey(key: string): Doc {
  return IDENT_RE.test(key) ? text(key) : text(`"${escapeString(key)}"`);
}

export function printExpr(e: Expr, ctx: number): Doc {
  switch (e.kind) {
    case 'num': {
      if (!Number.isFinite(e.value)) {
        throw new Error(`cannot print non-finite number: ${e.value}`);
      }
      const s = String(e.value);
      // A negative literal in callee/member-object position needs parens so
      // the postfix operator does not bind to the digits first.
      return e.value < 0 && ctx >= POSTFIX_PREC ? text(`(${s})`) : text(s);
    }
    case 'str':
      return text(`"${escapeString(e.value)}"`);
    case 'ident':
      return text(e.name);
    case 'unary': {
      const sep = WORD_UNARY.has(e.op) ? ' ' : '';
      const inner = printExpr(e.argument, UNARY_PREC);
      const arg =
        e.argument.kind === 'unary'
          ? concat([text('('), inner, text(')')])
          : inner;
      return parenIf(concat([text(e.op + sep), arg]), UNARY_PREC < ctx);
    }
    case 'binary':
      return printBinary(e, ctx);
    case 'cond': {
      const doc = group(
        printExpr(e.test, TERNARY_PREC + 1),
        indent(
          line,
          text('? '),
          printExpr(e.consequent, 0),
          line,
          text(': '),
          printExpr(e.alternate, TERNARY_PREC),
        ),
      );
      return parenIf(doc, TERNARY_PREC < ctx);
    }
    case 'call':
    case 'member':
    case 'index':
      return printPostfix(e, ctx);
    case 'array': {
      if (e.elements.length === 0) return text('[]');
      return group(
        text('['),
        indent(softline, join(concat([text(','), line]), e.elements.map((el) => printExpr(el, 0)))),
        softline,
        text(']'),
      );
    }
    case 'object': {
      if (e.props.length === 0) return text('{}');
      const props = e.props.map((p) =>
        concat([printKey(p.key), text(': '), printExpr(p.value, 0)]),
      );
      return group(text('{'), indent(line, join(concat([text(','), line]), props)), line, text('}'));
    }
  }
}

type Binary = Extract<Expr, { kind: 'binary' }>;

function printBinary(e: Binary, ctx: number): Doc {
  const p = BINARY_PREC[e.op];
  const operands: Expr[] = [];
  const ops: string[] = [];
  if (e.op === '**') {
    // Right-associative: no left-spine flattening.
    operands.push(e.left, e.right);
    ops.push('**');
  } else {
    // Flatten the left spine of same-precedence left-associative operators
    // so a long chain breaks as one group instead of deeply nested groups.
    let cur: Expr = e;
    while (cur.kind === 'binary' && cur.op !== '**' && BINARY_PREC[cur.op] === p) {
      operands.unshift(cur.right);
      ops.unshift(cur.op);
      cur = cur.left;
    }
    operands.unshift(cur);
  }
  const leftCtx = e.op === '**' ? p + 1 : p;
  const rightCtx = e.op === '**' ? p : p + 1;
  const segs: Doc[] = [];
  for (let i = 1; i < operands.length; i++) {
    segs.push(text(` ${ops[i - 1]}`), line, printExpr(operands[i], rightCtx));
  }
  const doc = group(printExpr(operands[0], leftCtx), indent(...segs));
  return parenIf(doc, p < ctx);
}

function printArgs(args: Expr[]): Doc {
  if (args.length === 0) return text('()');
  return group(
    text('('),
    indent(softline, join(concat([text(','), line]), args.map((a) => printExpr(a, 0)))),
    softline,
    text(')'),
  );
}

function printPostfix(e: Expr, ctx: number): Doc {
  // Unwind a postfix chain (calls, member access, indexing) so the whole
  // chain shares one group and breaks uniformly at the `.` joints.
  const parts: Doc[] = [];
  let base: Expr = e;
  let hasMemberPart = false;
  for (;;) {
    if (base.kind === 'call') {
      parts.unshift(printArgs(base.args));
      base = base.callee;
    } else if (base.kind === 'member') {
      hasMemberPart = true;
      parts.unshift(softline, text(`.${base.property}`));
      base = base.object;
    } else if (base.kind === 'index') {
      hasMemberPart = true;
      parts.unshift(
        group(text('['), indent(softline, printExpr(base.index, 0)), softline, text(']')),
      );
      base = base.object;
    } else {
      break;
    }
  }
  const doc = group(
    printExpr(base, POSTFIX_PREC),
    hasMemberPart ? indent(...parts) : concat(parts),
  );
  return parenIf(doc, POSTFIX_PREC < ctx);
}
