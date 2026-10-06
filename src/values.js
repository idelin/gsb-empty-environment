/**
 * 有限对象值集合：运行侧语义的载体。
 * 值为普通 JS 对象，字段值为 string/number/boolean 字面值。
 */
import { literal, object } from './types.js';

/**
 * 计算一个运行值的精确（exact）类型：
 * 原始值 -> 字面值类型；对象 -> 封闭对象类型（字段逐一取精确类型）。
 * @param {*} v @returns {import('./types.js').Type}
 */
export function typeOfValue(v) {
  if (v !== null && typeof v === 'object') {
    const fields = {};
    for (const [k, val] of Object.entries(v)) fields[k] = typeOfValue(val);
    return object(fields);
  }
  return literal(v);
}
