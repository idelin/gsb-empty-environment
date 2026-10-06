/**
 * 静态分析器：
 * - getField：字段访问检查，任一联合成员缺少该字段即报“未知字段”诊断；
 * - narrowEq：按 `field === literal` 等值条件收窄，then/else 双侧给出剩余类型；
 * - join：分支合流取联合；
 * - analyzeIf：完整 if 分析，不可达分支（剩余类型为 never）单独标记。
 */
import { NEVER, union, typeToString } from './types.js';

/**
 * @typedef {{ code: 'unknown-field', field: string, type: string, message: string }} Diagnostic
 * @typedef {{ field: string, equals: string|number|boolean }} EqCondition
 * @typedef {{ type: import('./types.js').Type, reachable: boolean }} Branch
 */

/**
 * 字段访问：返回字段类型；任一成员缺少该字段时记录诊断（未知字段访问报错）。
 * @param {import('./types.js').Type} type
 * @param {string} field
 * @param {Diagnostic[]} [diagnostics]
 * @returns {import('./types.js').Type} 字段类型（出错时为 never）
 */
export function getField(type, field, diagnostics = []) {
  const members = type.kind === 'union' ? type.members : [type];
  const parts = [];
  for (const m of members) {
    if (m.kind === 'object' && field in m.fields) {
      parts.push(m.fields[field]);
    } else {
      diagnostics.push({
        code: 'unknown-field',
        field,
        type: typeToString(m),
        message: `未知字段访问：类型 ${typeToString(m)} 上不存在字段 "${field}"`,
      });
    }
  }
  return parts.length === 0 ? NEVER : union(parts);
}

/**
 * 把字段类型按字面值等值拆成 匹配/剩余 两部分。
 * @param {import('./types.js').Type} fieldType
 * @param {string|number|boolean} lit
 */
function splitFieldType(fieldType, lit) {
  const members = fieldType.kind === 'union' ? fieldType.members : [fieldType];
  const match = [];
  const rest = [];
  for (const m of members) {
    if (m.kind === 'literal' && m.value === lit) match.push(m);
    else rest.push(m);
  }
  return { match: union(match), rest: union(rest) };
}

/**
 * 按 `field === lit` 收窄类型。
 * 语义与运行侧一致：字段缺失的成员在 then 侧被排除、else 侧保留；
 * 字段类型为字面值联合时，对象在两侧分别以匹配/剩余字段类型保留。
 * @param {import('./types.js').Type} type
 * @param {string} field
 * @param {string|number|boolean} lit
 * @returns {{ then: import('./types.js').Type, else: import('./types.js').Type }}
 */
export function narrowEq(type, field, lit) {
  const members = type.kind === 'union' ? type.members : [type];
  const thenParts = [];
  const elseParts = [];
  for (const m of members) {
    if (m.kind !== 'object') {
      // 非对象成员：字段读取为 undefined，等值不成立
      elseParts.push(m);
      continue;
    }
    if (!(field in m.fields)) {
      // 缺失 tag：等值不成立，仅落入 else
      elseParts.push(m);
      continue;
    }
    const { match, rest } = splitFieldType(m.fields[field], lit);
    if (match.kind !== 'never') {
      thenParts.push({ kind: 'object', fields: { ...m.fields, [field]: match } });
    }
    if (rest.kind !== 'never') {
      elseParts.push({ kind: 'object', fields: { ...m.fields, [field]: rest } });
    }
  }
  return { then: union(thenParts), else: union(elseParts) };
}

/**
 * 分支合流：取联合。
 * @param {import('./types.js').Type[]} branchTypes
 * @returns {import('./types.js').Type}
 */
export function join(...branchTypes) {
  return union(branchTypes);
}

/**
 * 完整 if 分析：双侧收窄 + 不可达标记 + 合流。
 * 若所有成员都不含该字段，附带 unknown-field 诊断。
 * @param {import('./types.js').Type} inputType
 * @param {EqCondition} cond
 * @returns {{ then: Branch, else: Branch, join: import('./types.js').Type, diagnostics: Diagnostic[] }}
 */
export function analyzeIf(inputType, cond) {
  const diagnostics = [];
  const members = inputType.kind === 'union' ? inputType.members : [inputType];
  if (inputType.kind !== 'never' &&
      !members.some((m) => m.kind === 'object' && cond.field in m.fields)) {
    diagnostics.push({
      code: 'unknown-field',
      field: cond.field,
      type: typeToString(inputType),
      message: `未知字段访问：类型 ${typeToString(inputType)} 上不存在字段 "${cond.field}"`,
    });
  }
  const { then, else: elseType } = narrowEq(inputType, cond.field, cond.equals);
  return {
    then: { type: then, reachable: then.kind !== 'never' },
    else: { type: elseType, reachable: elseType.kind !== 'never' },
    join: join(then, elseType),
    diagnostics,
  };
}
