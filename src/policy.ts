/**
 * 声明策略求值:allOf/anyOf 嵌套的声明条件。
 *
 * 安全不变量:
 * - 声明缺失一律视为不满足(拒绝默认)。
 * - 类型必须严格匹配:字符串 "false" 不等于布尔 false,"1" 不等于 1。
 * - TCB 版本按分量比较,每个分量都不得低于下限。
 */

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

export type Claims = { [key: string]: JsonValue };

/** TCB 版本:各平台分量组成的数字数组(如 SEV-SNP 的 [spl, snp, tee, ..., ucode])。 */
export type TcbVersion = number[];

export type Policy =
  | { allOf: Policy[] }
  | { anyOf: Policy[] }
  | { claim: string; equals: JsonValue }
  | { claim: string; tcbAtLeast: TcbVersion };

/** 按点分路径取声明,如 "x-ms-isolation-tee.x-ms-compliance-status"。 */
export function getClaim(claims: Claims, path: string): JsonValue | undefined {
  let current: JsonValue | undefined = claims;
  for (const segment of path.split(".")) {
    if (
      current === null ||
      typeof current !== "object" ||
      Array.isArray(current)
    ) {
      return undefined;
    }
    current = (current as { [key: string]: JsonValue })[segment];
    if (current === undefined) {
      return undefined;
    }
  }
  return current;
}

/** JSON 值的严格相等:类型不同(含 "false" vs false)一律不等。 */
export function strictJsonEquals(a: JsonValue, b: JsonValue): boolean {
  if (typeof a !== typeof b) {
    return false;
  }
  if (a === null || b === null) {
    return a === b;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
      return false;
    }
    return a.every((item, index) => strictJsonEquals(item, b[index]));
  }
  if (typeof a === "object") {
    const aObj = a as { [key: string]: JsonValue };
    const bObj = b as { [key: string]: JsonValue };
    const aKeys = Object.keys(aObj);
    const bKeys = Object.keys(bObj);
    if (aKeys.length !== bKeys.length) {
      return false;
    }
    return aKeys.every(
      (key) =>
        Object.prototype.hasOwnProperty.call(bObj, key) &&
        strictJsonEquals(aObj[key], bObj[key]),
    );
  }
  return a === b;
}

function isTcbVersion(value: JsonValue): value is JsonValue[] {
  return (
    Array.isArray(value) &&
    value.length > 0 &&
    value.every((component) => Number.isInteger(component) && component >= 0)
  );
}

/** actual 的每个分量都不得低于 minimum 的对应分量。 */
export function tcbSatisfies(
  actual: JsonValue | undefined,
  minimum: TcbVersion,
): boolean {
  if (actual === undefined || !isTcbVersion(actual)) {
    return false;
  }
  if (actual.length !== minimum.length) {
    return false;
  }
  return (actual as number[]).every(
    (component, index) => component >= minimum[index],
  );
}

export function evaluatePolicy(policy: Policy, claims: Claims): boolean {
  if ("allOf" in policy) {
    return policy.allOf.every((child) => evaluatePolicy(child, claims));
  }
  if ("anyOf" in policy) {
    return policy.anyOf.some((child) => evaluatePolicy(child, claims));
  }
  if ("tcbAtLeast" in policy) {
    return tcbSatisfies(getClaim(claims, policy.claim), policy.tcbAtLeast);
  }
  const actual = getClaim(claims, policy.claim);
  if (actual === undefined) {
    return false;
  }
  return strictJsonEquals(actual, policy.equals);
}
