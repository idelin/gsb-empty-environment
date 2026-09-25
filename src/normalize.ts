import type {
  FieldError,
  FieldIdentity,
  FieldRecord,
  JsonValue,
  NormalizeResult,
  Rule,
  RuleChange,
  RuleSet,
} from "./types.ts";
import { getByPointer, matchPattern, setByPointer, walkLeaves } from "./pointer.ts";
import { analyzeRuleset } from "./dependency.ts";
import { applyDate, applyEnum, applyWhitespace } from "./rules.ts";

export interface NormalizeOptions {
  /** 静态分析不通过时是否仍尝试执行（默认 false：拒绝执行并抛出）。 */
  force?: boolean;
  rulesetVersion?: string | null;
}

function fingerprint(value: JsonValue): string {
  if (value === null) return "null:";
  if (typeof value === "string") return `str:${value}`;
  return `json:${JSON.stringify(value)}`;
}

export function identityOf(path: string, value: JsonValue): FieldIdentity {
  const kind: FieldIdentity["kind"] =
    typeof value === "string" ? "string" : value === null ? "null" : "other";
  return { path, value, kind, fingerprint: fingerprint(value) };
}

function applyOne(rule: Rule, value: string): { ok: true; value: string } | { ok: false; code: string; message: string } {
  switch (rule.type) {
    case "whitespace":
      return applyWhitespace(rule, value);
    case "date":
      return applyDate(rule, value);
    case "enum":
      return applyEnum(rule, value);
  }
}

/**
 * 规范化 JSON 兼容树。
 *
 * 规则只作用于字符串叶子；null 与其他类型不参与（字符串空白与 null 从不等同）。
 * 某条规则在字段上失败时：保留当前原值、记录可定位错误，后续规则不再作用该字段。
 * 规范化两遍不会继续改变（幂等性由 searchIdempotency 在发布前验证）。
 */
export function normalize(
  input: JsonValue,
  rs: RuleSet,
  options: NormalizeOptions = {},
): NormalizeResult {
  const analysis = analyzeRuleset(rs);
  if (!analysis.ok && !options.force) {
    throw new Error(`规则集静态分析未通过，拒绝规范化:\\n- ${analysis.errors.join("\\n- ")}`);
  }

  const output: JsonValue = structuredClone(input);
  const records: Record<string, FieldRecord> = {};
  const errors: FieldError[] = [];
  const inversePatch: NormalizeResult["inversePatch"] = [];

  // 预计算每个具体叶子路径上的命中规则（数组顺序即显式管道顺序）。
  const leafPaths: string[] = [];
  walkLeaves(input, (path) => leafPaths.push(path));

  for (const path of leafPaths) {
    const originalValue = getByPointer(input, path) as JsonValue;
    const before = identityOf(path, originalValue);
    const changes: RuleChange[] = [];
    let fieldError: FieldError | undefined;

    if (typeof originalValue === "string") {
      let current = originalValue;
      for (const rule of rs.rules) {
        if (!matchPattern(rule.path, path)) continue;
        const from = current;
        const result = applyOne(rule, current);
        if (!result.ok) {
          fieldError = {
            path,
            ruleId: rule.id,
            ruleType: rule.type,
            code: result.code as FieldError["code"],
            message: result.message,
            retained: originalValue,
          };
          errors.push(fieldError);
          current = originalValue; // 出错：回退并保留原始值
          break;
        }
        if (result.value !== from) {
          changes.push({ ruleId: rule.id, ruleType: rule.type, from, to: result.value });
          current = result.value;
        }
      }
      if (current !== originalValue) {
        setByPointer(output, path, current);
        inversePatch.push({ op: "replace", path, value: originalValue });
      }
    }

    const finalValue = getByPointer(output, path) as JsonValue;
    records[path] = {
      before,
      after: identityOf(path, finalValue),
      changes,
      unchanged: changes.length === 0,
      error: fieldError,
    };
  }

  return {
    output,
    records,
    errors,
    inversePatch,
    rulesetVersion: options.rulesetVersion ?? null,
  };
}

/** 应用逆补丁到规范化结果，恢复规范化前的树（返回新树，不改原对象）。 */
export function applyInversePatch(normalized: JsonValue, patch: NormalizeResult["inversePatch"]): JsonValue {
  const restored: JsonValue = structuredClone(normalized);
  for (const op of patch) {
    if (op.op === "replace") setByPointer(restored, op.path, op.value);
  }
  return restored;
}

/** 便捷断言：第二遍规范化与第一遍输出完全相同。 */
export function isStable(input: JsonValue, rs: RuleSet): boolean {
  const first = normalize(input, rs).output;
  const second = normalize(first, rs).output;
  return JSON.stringify(first) === JSON.stringify(second);
}

