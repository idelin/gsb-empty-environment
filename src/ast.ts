// ---------------------------------------------------------------------------
// 表达式语言：常量、变量、算术（含可抛错的除法）、短路逻辑、条件表达式。
// 每个源节点在构建时分配稳定 ID，优化前后 ID 不变，用于错误定位与来源对应。
// ---------------------------------------------------------------------------

export type Value = number | boolean;

export type BinOp = '+' | '-' | '*' | '/';
export type LogicOp = '&&' | '||';

export type Node =
  | { kind: 'const'; id: number; value: Value }
  | { kind: 'var'; id: number; name: string }
  | { kind: 'bin'; id: number; op: BinOp; left: Node; right: Node }
  | { kind: 'logic'; id: number; op: LogicOp; left: Node; right: Node }
  | { kind: 'cond'; id: number; cond: Node; then: Node; els: Node }
  // 仅由优化器产生：常量折叠发现除零时，保留一个“到此处才抛错”的节点，
  // 从而保留原求值顺序与首个错误（ID 即原除法节点的 ID）。
  | { kind: 'error'; id: number };

// ---- 语义辅助（求值器与折叠器共用，保证两处语义一致） ----------------------

export function truthy(v: Value): boolean {
  return v !== false && v !== 0;
}

export function toNum(v: Value): number {
  return v === true ? 1 : v === false ? 0 : v;
}

export type BinResult = { ok: true; value: number } | { ok: false; divByZero: true };

export function computeBin(op: BinOp, l: Value, r: Value): BinResult {
  const a = toNum(l);
  const b = toNum(r);
  switch (op) {
    case '+': return { ok: true, value: a + b };
    case '-': return { ok: true, value: a - b };
    case '*': return { ok: true, value: a * b };
    case '/':
      if (b === 0) return { ok: false, divByZero: true };
      return { ok: true, value: a / b };
  }
}

// ---- 稳定 ID 分配器 --------------------------------------------------------

export class Builder {
  private nextId = 1;
  const(value: Value): Node { return { kind: 'const', id: this.nextId++, value }; }
  var(name: string): Node { return { kind: 'var', id: this.nextId++, name }; }
  bin(op: BinOp, left: Node, right: Node): Node {
    return { kind: 'bin', id: this.nextId++, op, left, right };
  }
  logic(op: LogicOp, left: Node, right: Node): Node {
    return { kind: 'logic', id: this.nextId++, op, left, right };
  }
  cond(cond: Node, then: Node, els: Node): Node {
    return { kind: 'cond', id: this.nextId++, cond, then, els };
  }
}

// ---- 解析器（便于书写测试用例；ID 按解析顺序分配，稳定可复现） --------------

export function parse(src: string): Node {
  const tokens = tokenize(src);
  const b = new Builder();
  let pos = 0;

  function peek(): string | undefined { return tokens[pos]; }
  function eat(expected?: string): string {
    const t = tokens[pos++];
    if (t === undefined) throw new Error('unexpected end of input');
    if (expected !== undefined && t !== expected) {
      throw new Error(`expected ${expected}, got ${t}`);
    }
    return t;
  }

  // cond := or ('?' cond ':' cond)?
  function parseCond(): Node {
    const c = parseOr();
    if (peek() === '?') {
      eat('?');
      const t = parseCond();
      eat(':');
      const e = parseCond();
      return b.cond(c, t, e);
    }
    return c;
  }
  function parseOr(): Node {
    let l = parseAnd();
    while (peek() === '||') { eat(); l = b.logic('||', l, parseAnd()); }
    return l;
  }
  function parseAnd(): Node {
    let l = parseAdd();
    while (peek() === '&&') { eat(); l = b.logic('&&', l, parseAdd()); }
    return l;
  }
  function parseAdd(): Node {
    let l = parseMul();
    while (peek() === '+' || peek() === '-') {
      const op = eat() as BinOp;
      l = b.bin(op, l, parseMul());
    }
    return l;
  }
  function parseMul(): Node {
    let l = parsePrimary();
    while (peek() === '*' || peek() === '/') {
      const op = eat() as BinOp;
      l = b.bin(op, l, parsePrimary());
    }
    return l;
  }
  function parsePrimary(): Node {
    const t = peek();
    if (t === '(') { eat('('); const e = parseCond(); eat(')'); return e; }
    if (t === 'true') { eat(); return b.const(true); }
    if (t === 'false') { eat(); return b.const(false); }
    if (t !== undefined && /^[0-9]/.test(t)) { eat(); return b.const(Number(t)); }
    if (t !== undefined && /^[a-zA-Z_]/.test(t)) { eat(); return b.var(t); }
    throw new Error(`unexpected token: ${t}`);
  }

  const root = parseCond();
  if (pos !== tokens.length) throw new Error(`trailing tokens: ${tokens.slice(pos)}`);
  return root;
}

function tokenize(src: string): string[] {
  const re = /\s*(\d+|[a-zA-Z_]\w*|&&|\|\||[()+\-*/?:])/y;
  const out: string[] = [];
  let i = 0;
  while (i < src.length) {
    re.lastIndex = i;
    const m = re.exec(src);
    if (!m) throw new Error(`bad token at ${i}: ${src.slice(i)}`);
    out.push(m[1]);
    i = re.lastIndex;
  }
  return out;
}

// ---- 打印（优化树带来源对应 src） ------------------------------------------

export function formatTree(node: Node, srcOf?: (n: Node) => number[]): string {
  const lines: string[] = [];
  const walk = (n: Node, depth: number): void => {
    const pad = '  '.repeat(depth);
    const src = srcOf ? `  <- src [${srcOf(n).join(', ')}]` : '';
    switch (n.kind) {
      case 'const': lines.push(`${pad}const ${JSON.stringify(n.value)}  #${n.id}${src}`); break;
      case 'var': lines.push(`${pad}var ${n.name}  #${n.id}${src}`); break;
      case 'error': lines.push(`${pad}ERROR DivByZero  #${n.id}${src}`); break;
      case 'bin':
        lines.push(`${pad}bin ${n.op}  #${n.id}${src}`);
        walk(n.left, depth + 1); walk(n.right, depth + 1); break;
      case 'logic':
        lines.push(`${pad}logic ${n.op}  #${n.id}${src}`);
        walk(n.left, depth + 1); walk(n.right, depth + 1); break;
      case 'cond':
        lines.push(`${pad}cond ?:  #${n.id}${src}`);
        walk(n.cond, depth + 1); walk(n.then, depth + 1); walk(n.els, depth + 1); break;
    }
  };
  walk(node, 0);
  return lines.join('\n');
}

export function collectVars(node: Node, into = new Set<string>()): Set<string> {
  switch (node.kind) {
    case 'var': into.add(node.name); break;
    case 'bin': case 'logic': collectVars(node.left, into); collectVars(node.right, into); break;
    case 'cond': collectVars(node.cond, into); collectVars(node.then, into); collectVars(node.els, into); break;
    default: break;
  }
  return into;
}
