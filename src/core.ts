// core.ts — AST with binding identity, surface syntax, s-expr parser, resolver, printer.

export type NodeId = number;
export type BindingId = number;

let nodeCounter = 1;
let bindCounter = 1;

export const freshNodeId = (): NodeId => nodeCounter++;
export const freshBindId = (): BindingId => bindCounter++;

export type PrimOp = '+' | '-' | '*' | '<' | '=' | 'not' | 'same?';
export const PRIMS: ReadonlySet<string> = new Set(['+', '-', '*', '<', '=', 'not', 'same?']);

export type Expr =
  | { k: 'lit'; id: NodeId; v: number | boolean }
  | { k: 'var'; id: NodeId; name: string; ref: BindingId }
  | { k: 'lam'; id: NodeId; param: string; bind: BindingId; body: Expr }
  | { k: 'app'; id: NodeId; fn: Expr; arg: Expr }
  | { k: 'let'; id: NodeId; name: string; bind: BindingId; value: Expr; body: Expr }
  | { k: 'if'; id: NodeId; c: Expr; t: Expr; e: Expr }
  | { k: 'prim'; id: NodeId; op: PrimOp; args: Expr[] }
  | { k: 'return'; id: NodeId; value: Expr };

export function children(e: Expr): Expr[] {
  switch (e.k) {
    case 'lit':
    case 'var':
      return [];
    case 'lam':
      return [e.body];
    case 'app':
      return [e.fn, e.arg];
    case 'let':
      return [e.value, e.body];
    case 'if':
      return [e.c, e.t, e.e];
    case 'prim':
      return e.args;
    case 'return':
      return [e.value];
  }
}

export function findNode(root: Expr, id: NodeId): Expr | undefined {
  if (root.id === id) return root;
  for (const c of children(root)) {
    const hit = findNode(c, id);
    if (hit) return hit;
  }
  return undefined;
}

export function nodeIds(e: Expr, out: NodeId[] = []): NodeId[] {
  out.push(e.id);
  for (const c of children(e)) nodeIds(c, out);
  return out;
}

// ---------- surface syntax ----------

export type Surface =
  | { k: 'lit'; v: number | boolean }
  | { k: 'var'; name: string }
  | { k: 'lam'; params: string[]; body: Surface }
  | { k: 'app'; fn: Surface; args: Surface[] }
  | { k: 'let'; defs: [string, Surface][]; body: Surface }
  | { k: 'if'; c: Surface; t: Surface; e: Surface }
  | { k: 'prim'; op: PrimOp; args: Surface[] }
  | { k: 'return'; value: Surface };

type SExpr = string | number | boolean | SExpr[];

export function parse(src: string): Surface {
  const toks = src.replace(/[()]/g, (m) => ` ${m} `).trim().split(/\s+/);
  let pos = 0;
  const peek = () => toks[pos];
  function parseOne(): SExpr {
    const t = toks[pos++];
    if (t === '(') {
      const list: SExpr[] = [];
      while (peek() !== ')') {
        if (pos >= toks.length) throw new Error('unclosed (');
        list.push(parseOne());
      }
      pos++;
      return list;
    }
    if (t === ')') throw new Error('unexpected )');
    if (/^-?\d+$/.test(t)) return Number(t);
    if (t === 'true') return true;
    if (t === 'false') return false;
    return t;
  }
  const se = parseOne();
  if (pos !== toks.length) throw new Error('trailing tokens');
  return surfaceFrom(se);
}

function surfaceFrom(se: SExpr): Surface {
  if (typeof se === 'number' || typeof se === 'boolean') return { k: 'lit', v: se };
  if (typeof se === 'string') return { k: 'var', name: se };
  const [head, ...rest] = se;
  if (typeof head !== 'string') {
    // ((lambda ...) arg...) — application with a computed operator
    return { k: 'app', fn: surfaceFrom(head), args: rest.map(surfaceFrom) };
  }
  switch (head) {
    case 'lambda': {
      const params = rest[0] as string[];
      return { k: 'lam', params, body: surfaceFrom(rest[1]) };
    }
    case 'let': {
      const defs = (rest[0] as [string, SExpr][]).map(
        ([n, v]) => [n, surfaceFrom(v)] as [string, Surface],
      );
      return { k: 'let', defs, body: surfaceFrom(rest[1]) };
    }
    case 'if':
      return { k: 'if', c: surfaceFrom(rest[0]), t: surfaceFrom(rest[1]), e: surfaceFrom(rest[2]) };
    case 'return':
      return { k: 'return', value: surfaceFrom(rest[0]) };
    default:
      if (PRIMS.has(head)) return { k: 'prim', op: head as PrimOp, args: rest.map(surfaceFrom) };
      return { k: 'app', fn: surfaceFrom(head), args: rest.map(surfaceFrom) };
  }
}

// ---------- resolver: names -> binding identities ----------

export interface Resolved {
  expr: Expr;
  globals: Map<string, BindingId>; // free variables, stable per name
}

export function resolve(s: Surface): Resolved {
  const globals = new Map<string, BindingId>();
  const scope = new Map<string, BindingId[]>();

  const push = (name: string, b: BindingId) => {
    const stack = scope.get(name) ?? [];
    stack.push(b);
    scope.set(name, stack);
  };
  const pop = (name: string) => {
    scope.get(name)!.pop();
  };
  const lookup = (name: string): BindingId => {
    const stack = scope.get(name);
    if (stack && stack.length > 0) return stack[stack.length - 1];
    let g = globals.get(name);
    if (g === undefined) {
      g = freshBindId();
      globals.set(name, g);
    }
    return g;
  };

  function go(s: Surface): Expr {
    switch (s.k) {
      case 'lit':
        return { k: 'lit', id: freshNodeId(), v: s.v };
      case 'var':
        return { k: 'var', id: freshNodeId(), name: s.name, ref: lookup(s.name) };
      case 'lam': {
        const binds = s.params.map(() => freshBindId());
        s.params.forEach((p, i) => push(p, binds[i]));
        const body = go(s.body);
        s.params.forEach((p) => pop(p));
        let out = body;
        for (let i = binds.length - 1; i >= 0; i--) {
          out = { k: 'lam', id: freshNodeId(), param: s.params[i], bind: binds[i], body: out };
        }
        return out;
      }
      case 'let': {
        // sequential (let*): each value resolved before its own binder is pushed
        const binds: [string, BindingId][] = [];
        const values: Expr[] = [];
        for (const [n, v] of s.defs) {
          values.push(go(v));
          const b = freshBindId();
          push(n, b);
          binds.push([n, b]);
        }
        let body = go(s.body);
        for (const [n] of binds) pop(n);
        for (let i = binds.length - 1; i >= 0; i--) {
          body = { k: 'let', id: freshNodeId(), name: binds[i][0], bind: binds[i][1], value: values[i], body };
        }
        return body;
      }
      case 'if':
        return { k: 'if', id: freshNodeId(), c: go(s.c), t: go(s.t), e: go(s.e) };
      case 'prim':
        return { k: 'prim', id: freshNodeId(), op: s.op, args: s.args.map(go) };
      case 'app': {
        let fn = go(s.fn);
        for (const a of s.args) fn = { k: 'app', id: freshNodeId(), fn, arg: go(a) };
        return fn;
      }
      case 'return':
        return { k: 'return', id: freshNodeId(), value: go(s.value) };
    }
  }

  return { expr: go(s), globals };
}

// ---------- printer (for debugging) ----------

export function show(e: Expr): string {
  switch (e.k) {
    case 'lit':
      return String(e.v);
    case 'var':
      return `${e.name}#${e.ref}`;
    case 'lam':
      return `(lambda (${e.param}#${e.bind}) ${show(e.body)})`;
    case 'app':
      return `(${show(e.fn)} ${show(e.arg)})`;
    case 'let':
      return `(let ((${e.name}#${e.bind} ${show(e.value)})) ${show(e.body)})`;
    case 'if':
      return `(if ${show(e.c)} ${show(e.t)} ${show(e.e)})`;
    case 'prim':
      return `(${e.op} ${e.args.map(show).join(' ')})`;
    case 'return':
      return `(return ${show(e.value)})`;
  }
}
