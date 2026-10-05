/**
 * 内存查询引擎：筛选 / 投影 / 连接，布尔结果采用三值逻辑（真 T / 假 F / 未知 U）。
 * NULL 与任何值（包括 NULL 自身）比较都得到 UNKNOWN；
 * 外连接补出的 NULL 与数据中的真实 NULL 走完全相同的求值路径。
 */

export type Value = number | string | boolean | null;
export type Row = Record<string, Value>;

export interface Table {
  cols: string[];
  rows: Row[];
}

// ---------------- 三值逻辑（Kleene 逻辑） ----------------

export type Tri = 'T' | 'F' | 'U';

export function triNot(a: Tri): Tri {
  return a === 'T' ? 'F' : a === 'F' ? 'T' : 'U';
}

export function triAnd(a: Tri, b: Tri): Tri {
  if (a === 'F' || b === 'F') return 'F';
  return a === 'T' && b === 'T' ? 'T' : 'U';
}

export function triOr(a: Tri, b: Tri): Tri {
  if (a === 'T' || b === 'T') return 'T';
  return a === 'F' && b === 'F' ? 'F' : 'U';
}

// ---------------- 表达式 ----------------

export type CmpOp = '=' | '<>' | '<' | '<=' | '>' | '>=';

export type Expr =
  | { kind: 'col'; name: string }
  | { kind: 'lit'; value: Value }
  | { kind: 'cmp'; op: CmpOp; left: Expr; right: Expr }
  | { kind: 'and'; left: Expr; right: Expr }
  | { kind: 'or'; left: Expr; right: Expr }
  | { kind: 'not'; expr: Expr }
  | { kind: 'isnull'; expr: Expr };

export const col = (name: string): Expr => ({ kind: 'col', name });
export const lit = (value: Value): Expr => ({ kind: 'lit', value });
export const cmp = (op: CmpOp, left: Expr, right: Expr): Expr => ({ kind: 'cmp', op, left, right });
export const and = (left: Expr, right: Expr): Expr => ({ kind: 'and', left, right });
export const or = (left: Expr, right: Expr): Expr => ({ kind: 'or', left, right });
export const not = (expr: Expr): Expr => ({ kind: 'not', expr });
export const isnull = (expr: Expr): Expr => ({ kind: 'isnull', expr });

function asTri(v: Value | Tri): Tri {
  if (v === 'T' || v === 'F' || v === 'U') return v;
  if (v === null) return 'U';
  if (typeof v === 'boolean') return v ? 'T' : 'F';
  throw new Error(`谓词位置出现非布尔值: ${JSON.stringify(v)}`);
}

function compare(op: CmpOp, l: Value, r: Value): Tri {
  // NULL 参与比较 → UNKNOWN（NULL = NULL 也是 UNKNOWN）
  if (l === null || r === null) return 'U';
  let res: boolean;
  switch (op) {
    case '=': res = l === r; break;
    case '<>': res = l !== r; break;
    case '<': res = (l as number) < (r as number); break;
    case '<=': res = (l as number) <= (r as number); break;
    case '>': res = (l as number) > (r as number); break;
    case '>=': res = (l as number) >= (r as number); break;
  }
  return res ? 'T' : 'F';
}

export function evalExpr(e: Expr, row: Row): Value | Tri {
  switch (e.kind) {
    case 'col': {
      const v = row[e.name];
      return v === undefined ? null : v;
    }
    case 'lit':
      return e.value;
    case 'cmp':
      return compare(e.op, evalExpr(e.left, row) as Value, evalExpr(e.right, row) as Value);
    case 'and':
      return triAnd(asTri(evalExpr(e.left, row)), asTri(evalExpr(e.right, row)));
    case 'or':
      return triOr(asTri(evalExpr(e.left, row)), asTri(evalExpr(e.right, row)));
    case 'not':
      return triNot(asTri(evalExpr(e.expr, row)));
    case 'isnull':
      return evalExpr(e.expr, row) === null ? 'T' : 'F';
  }
}

export function evalPred(p: Expr, row: Row): Tri {
  return asTri(evalExpr(p, row));
}

// ---------------- 算子 ----------------

/** 筛选：只保留谓词为“真”的行（假和未知都丢弃）。 */
export function filter(t: Table, p: Expr): Table {
  return { cols: t.cols, rows: t.rows.filter((r) => evalPred(p, r) === 'T') };
}

/** 投影：只保留指定列。 */
export function project(t: Table, cols: string[]): Table {
  return {
    cols,
    rows: t.rows.map((r) => {
      const out: Row = {};
      for (const c of cols) {
        const v = r[c];
        out[c] = v === undefined ? null : v;
      }
      return out;
    }),
  };
}

export type JoinKind = 'inner' | 'left' | 'right' | 'full';

/**
 * 连接（嵌套循环）。连接谓词按三值逻辑求值，只有“真”才匹配。
 * 外连接未匹配的一侧用 NULL 补齐，补出的 NULL 与真实 NULL 不加区分。
 * 约定两侧列名不相交。
 */
export function join(l: Table, r: Table, on: Expr, kind: JoinKind): Table {
  const cols = [...l.cols, ...r.cols];
  const rows: Row[] = [];
  const matchedL = new Array<boolean>(l.rows.length).fill(false);
  const matchedR = new Array<boolean>(r.rows.length).fill(false);

  for (let i = 0; i < l.rows.length; i++) {
    for (let j = 0; j < r.rows.length; j++) {
      const merged: Row = { ...l.rows[i], ...r.rows[j] };
      if (evalPred(on, merged) === 'T') {
        rows.push(merged);
        matchedL[i] = true;
        matchedR[j] = true;
      }
    }
  }

  const pad = (cs: string[]): Row => {
    const o: Row = {};
    for (const c of cs) o[c] = null;
    return o;
  };

  if (kind === 'left' || kind === 'full') {
    l.rows.forEach((row, i) => {
      if (!matchedL[i]) rows.push({ ...row, ...pad(r.cols) });
    });
  }
  if (kind === 'right' || kind === 'full') {
    r.rows.forEach((row, j) => {
      if (!matchedR[j]) rows.push({ ...pad(l.cols), ...row });
    });
  }
  return { cols, rows };
}
