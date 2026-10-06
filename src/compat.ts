/**
 * 模式兼容性判定：旧模式的所有合法记录是否都能被新模式接受。
 *
 * 判定方式是构造性的：递归比较两个模式，在每个失配点上
 * 从有限候选域（枚举差集、null、缺省字段、数组长度边界、
 * 嵌套子反例）中枚举候选反例，返回规模最小的具体记录。
 *
 * 结构性假设：
 * - 对象默认开放：旧记录多出的字段被新模式忽略；
 *   可用 { allowExtraFields: false } 切换为封闭对象。
 * - 新增必填字段不可忽略：新模式要求的字段若旧记录可能缺省，即不兼容。
 */

import {
  arrayMaxLen,
  arrayMinLen,
  minValue,
  validate,
  valueSize,
} from "./schema.ts";
import type { Schema } from "./schema.ts";

export interface CheckOptions {
  /** true（默认）：旧记录允许携带新模式未声明的字段 */
  readonly allowExtraFields?: boolean;
}

export type CompatResult =
  | { readonly compatible: true }
  | {
      readonly compatible: false;
      /** 最小反例：被旧模式接受、被新模式拒绝的具体记录 */
      readonly witness: unknown;
      /** 失配发生的 JSON 路径，如 "$.meta.tags[]" */
      readonly path: string;
      readonly reason: string;
    };

interface Hit {
  readonly witness: unknown;
  readonly path: string;
  readonly reason: string;
}

export function checkCompatibility(
  oldSchema: Schema,
  newSchema: Schema,
  options: CheckOptions = {},
): CompatResult {
  const hit = find(oldSchema, newSchema, "$", options);
  return hit ? { compatible: false, ...hit } : { compatible: true };
}

/** 递归寻找反例；返回 null 表示该层完全兼容 */
function find(
  oldS: Schema,
  newS: Schema,
  path: string,
  options: CheckOptions,
): Hit | null {
  // 空枚举域没有任何合法值，与任何模式兼容
  if (oldS.kind === "enum" && oldS.values.length === 0) return null;

  // --- nullable：值级可空，先于结构分派处理 ---
  if (oldS.kind === "nullable") {
    if (newS.kind === "nullable") {
      return find(oldS.inner, newS.inner, path, options);
    }
    // 旧记录可以取 null，新模式不接受 null
    return { witness: null, path, reason: "old schema allows null, new schema does not" };
  }
  if (newS.kind === "nullable") {
    // 旧值非 null，只需被 newS.inner 接受
    return find(oldS, newS.inner, path, options);
  }

  // --- 对象 ---
  if (oldS.kind === "object" && newS.kind === "object") {
    return findInObject(oldS, newS, path, options);
  }

  // --- 数组 ---
  if (oldS.kind === "array" && newS.kind === "array") {
    return findInArray(oldS, newS, path, options);
  }

  // --- 枚举域 ---
  if (oldS.kind === "enum" && newS.kind === "enum") {
    const dropped = oldS.values.find((v) => !newS.values.some((w) => w === v));
    if (dropped === undefined) return null;
    return {
      witness: dropped,
      path,
      reason: `enum value ${JSON.stringify(dropped)} removed in new schema`,
    };
  }
  if (oldS.kind === "enum") {
    // 枚举 ⊆ 原始类型：全部值都落在目标类型才兼容
    const target = newS.kind;
    if (target === "string" || target === "number" || target === "boolean") {
      const bad = oldS.values.find((v) => typeof v !== target);
      if (bad === undefined) return null;
      return {
        witness: bad,
        path,
        reason: `enum value ${JSON.stringify(bad)} is not a ${target}`,
      };
    }
    return { witness: oldS.values[0], path, reason: kindMismatch(oldS, newS) };
  }
  if (newS.kind === "enum") {
    // 无限域 → 有限枚举：枚举不在集合内的最小代表值
    const witness = minValueOutsideEnum(oldS, newS.values);
    return {
      witness,
      path,
      reason: `new schema narrows ${oldS.kind} to a finite enum`,
    };
  }

  // --- 原始类型 ---
  if (oldS.kind === newS.kind) return null;
  return { witness: minValue(oldS), path, reason: kindMismatch(oldS, newS) };
}

function findInObject(
  oldS: Extract<Schema, { kind: "object" }>,
  newS: Extract<Schema, { kind: "object" }>,
  path: string,
  options: CheckOptions,
): Hit | null {
  const candidates: Hit[] = [];
  const baseRecord = (): Record<string, unknown> => minValue(oldS) as Record<string, unknown>;

  // 1) 新增/强化的必填字段：旧记录可能根本不带这个字段
  for (const [name, newField] of Object.entries(newS.fields)) {
    if (!newField.required) continue;
    const oldField = oldS.fields[name];
    if (oldField && oldField.required) continue;
    const witness = baseRecord();
    delete witness[name]; // 确保缺省（旧模式里它至多可选）
    candidates.push({
      witness,
      path: `${path}.${name}`,
      reason: oldField
        ? `field "${name}" was optional and is now required`
        : `new required field "${name}" cannot be ignored`,
    });
  }

  // 2) 共有字段的类型兼容
  for (const [name, oldField] of Object.entries(oldS.fields)) {
    const newField = newS.fields[name];
    if (!newField) continue;
    const sub = find(oldField.schema, newField.schema, `${path}.${name}`, options);
    if (!sub) continue;
    const witness = baseRecord();
    witness[name] = sub.witness; // 可选字段带上坏值仍是旧模式合法记录
    candidates.push({ witness, path: sub.path, reason: sub.reason });
  }

  // 3) 封闭对象模式下，旧模式多出的字段
  if (options.allowExtraFields === false) {
    for (const [name, oldField] of Object.entries(oldS.fields)) {
      if (newS.fields[name]) continue;
      const witness = baseRecord();
      witness[name] = minValue(oldField.schema);
      candidates.push({
        witness,
        path: `${path}.${name}`,
        reason: `field "${name}" is not declared in the new (closed) schema`,
      });
    }
  }

  return pickMinimal(candidates);
}

function findInArray(
  oldS: Extract<Schema, { kind: "array" }>,
  newS: Extract<Schema, { kind: "array" }>,
  path: string,
  options: CheckOptions,
): Hit | null {
  const candidates: Hit[] = [];
  const oMin = arrayMinLen(oldS);
  const oMax = arrayMaxLen(oldS);
  const nMin = arrayMinLen(newS);
  const nMax = arrayMaxLen(newS);
  const minItem = minValue(oldS.items);
  const arrayOf = (n: number, fill: unknown = minItem): unknown[] =>
    Array.from({ length: n }, () => fill);

  // 1) 长度下界抬高：旧模式允许的更短数组
  if (nMin > oMin && oMin <= oMax) {
    candidates.push({
      witness: arrayOf(oMin),
      path,
      reason: `array shorter than new minLen ${nMin} was allowed`,
    });
  }

  // 2) 长度上界压低：旧模式允许的更长数组
  //    nMax < oMax 蕴含 nMax 有限，从而 tooLong 是合法长度
  const tooLong = nMax + 1;
  if (nMax < oMax && tooLong >= oMin) {
    candidates.push({
      witness: arrayOf(tooLong),
      path,
      reason: `array longer than new maxLen ${nMax} was allowed`,
    });
  }

  // 3) 元素类型兼容：最短合法数组装入元素反例
  const sub = find(oldS.items, newS.items, `${path}[]`, options);
  if (sub) {
    const len = Math.max(oMin, 1);
    if (len <= oMax) {
      const witness = arrayOf(len);
      witness[0] = sub.witness;
      candidates.push({ witness, path: sub.path, reason: sub.reason });
    }
  }

  return pickMinimal(candidates);
}

/** 旧类型为无限域、新模式收窄为有限枚举时，构造不在枚举内的最小代表值 */
function minValueOutsideEnum(
  oldS: Schema,
  values: readonly (string | number)[],
): unknown {
  if (oldS.kind === "number") {
    const nums = new Set(values.filter((v) => typeof v === "number"));
    let candidate = 0;
    while (nums.has(candidate)) candidate += 1;
    return candidate;
  }
  if (oldS.kind === "boolean") return false; // 枚举值域不含 boolean
  // string（及其他情况）：按长度枚举最短的不在集合内的字符串
  const strings = new Set(values.filter((v) => typeof v === "string"));
  const alphabet = "abcdefghijklmnopqrstuvwxyz";
  let current = [""];
  for (let depth = 0; depth < 4; depth += 1) {
    const next: string[] = [];
    for (const s of current) {
      if (!strings.has(s)) return s;
      for (const ch of alphabet) next.push(s + ch);
    }
    current = next;
  }
  return "__not_in_enum__";
}

function kindMismatch(oldS: Schema, newS: Schema): string {
  return `type mismatch: ${oldS.kind} is not assignable to ${newS.kind}`;
}

function pickMinimal(candidates: Hit[]): Hit | null {
  let best: Hit | null = null;
  for (const hit of candidates) {
    if (!best || valueSize(hit.witness) < valueSize(best.witness)) best = hit;
  }
  return best;
}

/** 便捷断言形式：兼容返回 true，否则抛出带最小反例的错误 */
export function assertCompatible(
  oldSchema: Schema,
  newSchema: Schema,
  options: CheckOptions = {},
): true {
  const result = checkCompatibility(oldSchema, newSchema, options);
  if (result.compatible) return true;
  throw new Error(
    `Incompatible schemas at ${result.path}: ${result.reason}\n` +
      `Minimal witness: ${JSON.stringify(result.witness)}`,
  );
}

export { validate };
