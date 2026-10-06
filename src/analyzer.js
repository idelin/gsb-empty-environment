/**
 * 静态分析器(TypeScript 风格的判别联合收窄)。
 *
 * 语义:
 * - tag 等值判断收窄: `value.tag === "x"` 的肯定分支只保留 tag 可能为 "x"
 *   的联合成员, 并把该字段收窄到字面值; 否定分支剔除该字面值。
 * - 缺失 tag: 可选字段(optional)或部分联合成员没有该字段时, 缺失值在肯定
 *   分支被排除(缺失 !== 字面值), 在否定分支保留。
 * - 未知字段访问报错: 字段在所有联合成员上都不存在时产生 UNKNOWN_FIELD 错误。
 * - 分支合流取联合: 所有叶子的剩余类型合并(residual)为规范化联合。
 * - 不可达分支单独标记: 剩余类型为 never 的叶子进入 unreachable, 不计入错误。
 */

import {
  never,
  union,
  obj,
  keepOnlyLiteral,
  removeLiteral,
  typeToString,
} from './types.js';
import { exprToString } from './ast.js';

/**
 * @param {import('./types.js').Type} inputType 输入值的静态类型
 * @param {object} program 分支决策树(ast.js 构造)
 * @returns {{
 *   input: import('./types.js').Type,
 *   leaves: Array<{ label: string, type: import('./types.js').Type, reachable: boolean, path: string[] }>,
 *   unreachable: Array<{ label: string, path: string[] }>,
 *   residual: import('./types.js').Type,
 *   diagnostics: Array<{ severity: 'error', code: 'UNKNOWN_FIELD', field: string, message: string }>,
 * }}
 */
export function analyze(inputType, program) {
  /** @type {Map<string, object>} */
  const diagnostics = new Map();
  const leaves = [];
  const unreachable = [];

  walk(inputType, program, []);

  const residual = union(...leaves.map((l) => l.type));
  return {
    input: inputType,
    leaves,
    unreachable,
    residual,
    diagnostics: [...diagnostics.values()],
  };

  function walk(type, node, path) {
    if (node.kind === 'leaf') {
      const reachable = type.kind !== 'never';
      const entry = { label: node.label, type, reachable, path };
      leaves.push(entry);
      if (!reachable) unreachable.push({ label: node.label, path });
      return;
    }
    checkCond(type, node.cond);
    const desc = exprToString(node.cond);
    walk(narrow(type, node.cond, true), node.then, [...path, `then(${desc})`]);
    walk(narrow(type, node.cond, false), node.else, [...path, `else(${desc})`]);
  }

  function report(d) {
    diagnostics.set(`${d.code}:${d.field}`, d);
  }

  function checkCond(type, cond) {
    if (type.kind === 'never') return;
    for (const g of collectGets(cond)) {
      const r = resolveField(type, g.field);
      if (!r.known) {
        report({
          severity: 'error',
          code: 'UNKNOWN_FIELD',
          field: g.field,
          message: `字段 "${g.field}" 在类型 ${typeToString(type)} 的任何成员上都不存在`,
        });
      }
    }
  }
}

/**
 * 解析字段在类型上的信息。
 * known: 至少一个联合成员声明了该字段(全部未知才算未知字段错误);
 * missing: 取值可能缺失(可选字段或部分成员无此字段)。
 */
function resolveField(t, fieldName) {
  switch (t.kind) {
    case 'never':
      return { known: true, type: never, missing: false };
    case 'literal':
      return { known: false, type: never, missing: true };
    case 'object': {
      const f = t.fields[fieldName];
      if (!f) return { known: false, type: never, missing: true };
      return { known: true, type: f.type, missing: f.optional };
    }
    case 'union': {
      const rs = t.members.map((m) => resolveField(m, fieldName));
      const known = rs.some((r) => r.known);
      const type = union(...rs.filter((r) => r.known).map((r) => r.type));
      const missing = rs.some((r) => r.missing || !r.known);
      return { known, type, missing };
    }
  }
}

/** 按 cond 的极性(positive=条件为真)收窄类型。 */
function narrow(t, cond, positive) {
  if (t.kind === 'never') return never;
  switch (cond.kind) {
    case 'lit': {
      const truthy = Boolean(cond.value);
      return truthy === positive ? t : never;
    }
    case 'not':
      return narrow(t, cond.cond, !positive);
    case 'and': {
      if (positive) return cond.conds.reduce((acc, c) => narrow(acc, c, true), t);
      return union(...cond.conds.map((c) => narrow(t, c, false)));
    }
    case 'or': {
      if (positive) return union(...cond.conds.map((c) => narrow(t, c, true)));
      return cond.conds.reduce((acc, c) => narrow(acc, c, false), t);
    }
    case 'eq': {
      const { left, right } = cond;
      if (left.kind === 'get' && left.of.kind === 'ref' && right.kind === 'lit') {
        return narrowEq(t, left.field, right.value, positive);
      }
      if (right.kind === 'get' && right.of.kind === 'ref' && left.kind === 'lit') {
        return narrowEq(t, right.field, left.value, positive);
      }
      if (left.kind === 'lit' && right.kind === 'lit') {
        return (left.value === right.value) === positive ? t : never;
      }
      return t;
    }
    default:
      return t;
  }
}

function narrowEq(t, fieldName, value, positive) {
  if (t.kind === 'union') {
    return union(...t.members.map((m) => narrowEqMember(m, fieldName, value, positive)));
  }
  if (t.kind === 'never') return never;
  return narrowEqMember(t, fieldName, value, positive);
}

function narrowEqMember(member, fieldName, value, positive) {
  if (member.kind !== 'object') return positive ? never : member;
  const f = member.fields[fieldName];
  if (!f) return positive ? never : member;
  if (positive) {
    const kept = keepOnlyLiteral(f.type, value);
    if (kept.kind === 'never') return never;
    return obj({ ...member.fields, [fieldName]: { type: kept, optional: false } });
  }
  const rest = removeLiteral(f.type, value);
  if (!f.optional && rest.kind === 'never') return never;
  return obj({ ...member.fields, [fieldName]: { type: rest, optional: f.optional } });
}

function collectGets(node, acc = []) {
  switch (node.kind) {
    case 'get':
      acc.push(node);
      collectGets(node.of, acc);
      break;
    case 'eq':
      collectGets(node.left, acc);
      collectGets(node.right, acc);
      break;
    case 'and':
    case 'or':
      for (const c of node.conds) collectGets(c, acc);
      break;
    case 'not':
      collectGets(node.cond, acc);
      break;
  }
  return acc;
}
