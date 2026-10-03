// 关联子查询解除（Subquery Unnesting / Decorrelation）
//
// 规则：
//   WHERE EXISTS (SELECT ... FROM t WHERE t.k = outer.k [AND 局部条件])
//     => FROM outer SEMI JOIN t ON t.k = outer.k [AND 局部条件]
//   WHERE NOT EXISTS (...)  => ANTI JOIN
//   WHERE expr IN (SELECT col FROM t [WHERE ...])
//     => SEMI JOIN t ON expr = t.col [AND 子查询条件]
//   WHERE expr NOT IN (...) => ANTI JOIN（注：NULL 语义需执行层保证子查询无 NULL）

import {
  andAll,
  splitAnd,
  type Expr,
  type SelectItem,
  type SelectStmt,
  type TableSource,
} from './ast.ts';

/** 收集 FROM 子句中暴露的所有表别名（未起别名时用表名） */
export function collectAliases(sources: TableSource[]): string[] {
  const out: string[] = [];
  const walk = (s: TableSource) => {
    if (s.kind === 'table') {
      out.push(s.alias ?? s.name);
    } else {
      walk(s.left);
      walk(s.right);
    }
  };
  sources.forEach(walk);
  return out;
}

/** 收集表达式中引用的全部表限定符 */
function referencedTables(expr: Expr, into: Set<string> = new Set()): Set<string> {
  switch (expr.kind) {
    case 'column':
      if (expr.table) into.add(expr.table);
      break;
    case 'binary':
      referencedTables(expr.left, into);
      referencedTables(expr.right, into);
      break;
    case 'exists':
      // 子查询内部列在 decorrelate 中单独处理，这里只关心直接引用
      break;
    case 'in':
      referencedTables(expr.expr, into);
      break;
    default:
      break;
  }
  return into;
}

function isCorrelated(expr: Expr, outerAliases: Set<string>): boolean {
  const refs = referencedTables(expr);
  for (const r of refs) if (outerAliases.has(r)) return true;
  return false;
}

/** 将子查询的 FROM 列表折叠为单个 TableSource（多表用 CROSS JOIN 连接） */
function foldSources(sources: TableSource[]): TableSource {
  if (sources.length === 1) return sources[0];
  return sources.slice(1).reduce<TableSource>(
    (acc, s) => ({ kind: 'join', joinType: 'CROSS', left: acc, right: s }),
    sources[0],
  );
}

interface Extracted {
  joinType: 'SEMI' | 'ANTI';
  source: TableSource;
  on: Expr;
}

/**
 * 尝试把一个 WHERE 合取项提取为 Semi/Anti Join。
 * 无法提取（普通谓词）时返回 null。
 */
function tryExtractJoin(conjunct: Expr, outerAliases: Set<string>): Extracted | null {
  if (conjunct.kind === 'exists') {
    const sub = decorrelate(conjunct.subquery); // 先处理嵌套子查询
    const conditions = sub.where ? splitAnd(sub.where) : [];
    // 相关谓词与局部谓词都可并入 Semi Join 的 ON：
    // 对 Semi Join 而言，ON 中的内表过滤与原语义等价。
    const on = andAll(conditions) ?? { kind: 'literal', value: true };
    return {
      joinType: conjunct.negated ? 'ANTI' : 'SEMI',
      source: foldSources(sub.from),
      on,
    };
  }

  if (conjunct.kind === 'in') {
    const sub = decorrelate(conjunct.subquery);
    if (sub.columns.length !== 1 || sub.columns[0].kind !== 'expr') {
      throw new Error('IN 子查询必须且只能投影一个表达式');
    }
    const target = sub.columns[0].expr;
    const conditions = sub.where ? splitAnd(sub.where) : [];
    const equality: Expr = { kind: 'binary', op: '=', left: conjunct.expr, right: target };
    const on = andAll([equality, ...conditions])!;
    return {
      joinType: conjunct.negated ? 'ANTI' : 'SEMI',
      source: foldSources(sub.from),
      on,
    };
  }

  return null;
}

/** 把 SELECT * 展开为外层各表的 t.*（Semi/Anti Join 只应输出左表列） */
function expandStar(columns: SelectItem[], outerSources: TableSource[]): SelectItem[] {
  const aliases = collectAliases(outerSources);
  const out: SelectItem[] = [];
  for (const c of columns) {
    if (c.kind === 'star' && !c.table) {
      for (const a of aliases) out.push({ kind: 'star', table: a });
    } else {
      out.push(c);
    }
  }
  return out;
}

/** 对单条 SELECT 做一层解除，并递归处理子查询内部 */
export function decorrelate(stmt: SelectStmt): SelectStmt {
  const from = [...stmt.from];
  let where = stmt.where;
  let columns = stmt.columns;

  if (where) {
    const outerAliases = new Set(collectAliases(from));
    const remaining: Expr[] = [];
    let current: TableSource = foldSources(from);
    let extracted = false;

    for (const conjunct of splitAnd(where)) {
      const join = tryExtractJoin(conjunct, outerAliases);
      if (join) {
        current = {
          kind: 'join',
          joinType: join.joinType,
          left: current,
          right: join.source,
          on: join.on,
        };
        extracted = true;
      } else {
        remaining.push(conjunct);
      }
    }

    if (extracted) {
      from.length = 0;
      from.push(current);
      columns = expandStar(columns, stmt.from);
      where = andAll(remaining);
    }
  }

  return { kind: 'select', columns, from, where };
}

/** 入口：解析 -> 解除关联 -> 返回重写后的 AST */
export function unnest(stmt: SelectStmt): SelectStmt {
  return decorrelate(stmt);
}

export { isCorrelated };
