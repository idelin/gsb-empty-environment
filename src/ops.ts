/**
 * 纯函数字段操作助手。
 * 重复执行已完成步骤时，这些助手不会重复追加字段（幂等保护的一部分）：
 * - addField：键已存在即报错（把重复应用显式暴露出来，而非静默叠加）。
 * - appendUnique：已存在等价元素则不再追加。
 */
import type { JsonObject, JsonValue } from './json.ts';
import { deepEqual } from './json.ts';

export function addField(data: JsonObject, key: string, value: JsonValue): JsonObject {
  if (key in data) {
    throw new Error(`refusing to add duplicate field "${key}" (field already exists)`);
  }
  return { ...data, [key]: value };
}

export function setField(data: JsonObject, key: string, value: JsonValue): JsonObject {
  return { ...data, [key]: value };
}

export function removeField(data: JsonObject, key: string): JsonObject {
  if (!(key in data)) return data;
  const next = { ...data };
  delete next[key];
  return next;
}

export function appendUnique(data: JsonObject, key: string, value: JsonValue): JsonObject {
  const current = data[key];
  if (current !== undefined && !Array.isArray(current)) {
    throw new Error(`cannot append: "${key}" is not an array`);
  }
  const list = (current ?? []) as JsonValue[];
  if (list.some((item) => deepEqual(item, value))) {
    return data;
  }
  return { ...data, [key]: [...list, value] };
}

/** 重命名字段；源不存在或目标已存在都报错。 */
export function renameField(data: JsonObject, fromKey: string, toKey: string): JsonObject {
  if (!(fromKey in data)) {
    throw new Error(`cannot rename: source field "${fromKey}" is missing`);
  }
  if (toKey in data) {
    throw new Error(`cannot rename: target field "${toKey}" already exists`);
  }
  const next: JsonObject = {};
  for (const [k, v] of Object.entries(data)) {
    if (k !== fromKey) next[k] = v;
  }
  next[toKey] = data[fromKey] as JsonValue;
  return next;
}
