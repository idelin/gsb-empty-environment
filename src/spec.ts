import type { Constraint, JsonValue, ParamSpec } from "./types.ts";
import { ExpandError } from "./errors.ts";

export interface NormalizedSpec {
  /** 轴名，按字典序排序（对象键无序，需规范化出确定顺序）。 */
  axisNames: string[];
  /** 与 axisNames 对齐的有序值域。 */
  domains: JsonValue[][];
  constraints: Constraint[];
}

function isPlainObject(v: JsonValue): v is Record<string, JsonValue> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function validateEqualities(
  value: JsonValue,
  path: string,
  axisSet: Set<string>
): Record<string, JsonValue> {
  if (!isPlainObject(value)) {
    throw new ExpandError(path, "expected an object of axis -> value equalities");
  }
  for (const key of Object.keys(value)) {
    if (!axisSet.has(key)) {
      throw new ExpandError(`${path}.${key}`, `unknown axis "${key}"`);
    }
  }
  return value;
}

/** 校验并规范化参数；所有错误都定位到具体路径。 */
export function normalizeParams(params: ParamSpec): NormalizedSpec {
  if (!isPlainObject(params as JsonValue)) {
    throw new ExpandError("$.params", "params must be an object");
  }
  const axes = (params as ParamSpec).axes;
  if (!isPlainObject(axes as JsonValue)) {
    throw new ExpandError("$.params.axes", "axes must be an object of name -> value array");
  }
  const axisNames = Object.keys(axes).sort();
  const axisSet = new Set(axisNames);
  const domains: JsonValue[][] = [];
  for (let i = 0; i < axisNames.length; i++) {
    const name = axisNames[i]!;
    const domain = (axes as Record<string, JsonValue>)[name];
    if (!Array.isArray(domain)) {
      throw new ExpandError(
        `$.params.axes.${name}`,
        `axis "${name}" domain must be an array (arrays are ordered)`
      );
    }
    domains.push(domain);
  }

  const rawConstraints = (params as ParamSpec).constraints ?? [];
  if (!Array.isArray(rawConstraints)) {
    throw new ExpandError("$.params.constraints", "constraints must be an array");
  }
  const constraints: Constraint[] = [];
  for (let i = 0; i < rawConstraints.length; i++) {
    const c = rawConstraints[i] as Constraint;
    const base = `$.params.constraints[${i}]`;
    if (!isPlainObject(c as JsonValue)) {
      throw new ExpandError(base, "constraint must be an object");
    }
    const hasForbid = "forbid" in c;
    const hasRequire = "require" in c;
    if (hasForbid === hasRequire) {
      throw new ExpandError(
        base,
        'constraint must be exactly one of { forbid: {...} } or { require: {...}, allow: {...} }'
      );
    }
    if (hasForbid) {
      const when = validateEqualities(
        (c as { forbid: Record<string, JsonValue> }).forbid,
        `${base}.forbid`,
        axisSet
      );
      if (Object.keys(when).length === 0) {
        throw new ExpandError(`${base}.forbid`, "forbid must mention at least one axis");
      }
      constraints.push({ forbid: when });
    } else {
      const req = (c as { require: Record<string, JsonValue>; allow: unknown }).require;
      const allow = (c as { allow: unknown }).allow;
      const when = validateEqualities(req, `${base}.require`, axisSet);
      if (Object.keys(when).length === 0) {
        throw new ExpandError(`${base}.require`, "require must mention at least one axis");
      }
      if (!isPlainObject(allow as JsonValue)) {
        throw new ExpandError(`${base}.allow`, "allow must be an object of axis -> allowed values");
      }
      const allowMap: Record<string, JsonValue[]> = {};
      for (const key of Object.keys(allow as Record<string, JsonValue>)) {
        if (!axisSet.has(key)) {
          throw new ExpandError(`${base}.allow.${key}`, `unknown axis "${key}"`);
        }
        const list = (allow as Record<string, JsonValue>)[key];
        if (!Array.isArray(list)) {
          throw new ExpandError(`${base}.allow.${key}`, "allowed values must be an array");
        }
        allowMap[key] = list;
      }
      if (Object.keys(allowMap).length === 0) {
        throw new ExpandError(`${base}.allow`, "allow must mention at least one axis");
      }
      constraints.push({ require: when, allow: allowMap });
    }
  }
  return { axisNames, domains, constraints };
}

/** 校验模板中的引用节点；返回是否含有引用。错误路径定位到模板内部。 */
export function validateTemplate(template: JsonValue, axisSet: Set<string>, path = "$.template"): void {
  if (Array.isArray(template)) {
    for (let i = 0; i < template.length; i++) {
      validateTemplate(template[i]!, axisSet, `${path}[${i}]`);
    }
    return;
  }
  if (typeof template === "object" && template !== null) {
    const keys = Object.keys(template);
    const refKey = keys.find((k) => k === "$ref" || k === "$index");
    if (refKey !== undefined) {
      if (keys.length !== 1) {
        throw new ExpandError(
          `${path}.${refKey}`,
          `a "${refKey}" node must not have sibling keys`
        );
      }
      const target = (template as Record<string, JsonValue>)[refKey];
      if (typeof target !== "string" || !axisSet.has(target)) {
        throw new ExpandError(
          `${path}.${refKey}`,
          `unknown axis ${JSON.stringify(target)}`
        );
      }
      return;
    }
    for (const key of keys) {
      validateTemplate((template as Record<string, JsonValue>)[key]!, axisSet, `${path}.${key}`);
    }
  }
}

/** 校验身份声明（点分路径列表）。 */
export function validateIdentity(identity: unknown): string[] {
  if (identity === undefined) return [];
  if (!Array.isArray(identity)) {
    throw new ExpandError("$.options.identity", "identity must be an array of dot-separated paths");
  }
  for (let i = 0; i < identity.length; i++) {
    const p = identity[i];
    if (typeof p !== "string" || p.length === 0 || p.split(".").some((s) => s.length === 0)) {
      throw new ExpandError(
        `$.options.identity[${i}]`,
        "identity entries must be non-empty dot-separated paths"
      );
    }
  }
  return identity as string[];
}
