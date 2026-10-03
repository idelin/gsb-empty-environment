import { Rng } from './rng.ts';
import { assertSupported, escapePointer, unescapePointer, type Schema } from './schema.ts';
import { parsePattern, generateFromPattern, ALPHABET } from './pattern.ts';
import { deepEqual, validate, type ValidationError } from './validator.ts';
import {
  Unsatisfiable,
  domainSize,
  generate,
  genDefaultValue,
  genNode,
  genObject,
  genString,
} from './generator.ts';

/** 某条具体规则的引用：schemaPath 指向规则本身（含关键字段）。 */
export interface RuleRef {
  schemaPath: string;
  keyword: string;
}

/** 无法构造时抛出（带人类可读原因），绝不伪造反例。 */
export class CannotConstruct extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CannotConstruct';
  }
}

export type CounterexampleResult =
  | { ok: true; instance: unknown; violations: ValidationError[] }
  | { ok: false; reason: string };

/** 枚举 schema 中所有可针对的规则（含嵌套 properties/items）。 */
export function listRules(schema: Schema): RuleRef[] {
  assertSupported(schema);
  const rules: RuleRef[] = [];
  const push = (path: string, keyword: string) =>
    rules.push({ schemaPath: `${path}/${keyword}`, keyword });

  const walk = (s: Schema, path: string): void => {
    if ('const' in s) push(path, 'const');
    if (s.enum !== undefined) push(path, 'enum');
    if (s.type !== undefined) push(path, 'type');
    for (const keyword of [
      'minimum',
      'maximum',
      'exclusiveMinimum',
      'exclusiveMaximum',
      'multipleOf',
      'minLength',
      'maxLength',
      'pattern',
      'minItems',
      'maxItems',
    ] as const) {
      if (s[keyword] !== undefined) push(path, keyword);
    }
    if (s.uniqueItems === true) push(path, 'uniqueItems');
    if (s.required !== undefined && s.required.length > 0) push(path, 'required');
    if (s.additionalProperties === false) push(path, 'additionalProperties');
    if (s.dependentRequired !== undefined && Object.keys(s.dependentRequired).length > 0) {
      push(path, 'dependentRequired');
    }
    for (const [key, sub] of Object.entries(s.properties ?? {})) {
      walk(sub, `${path}/properties/${escapePointer(key)}`);
    }
    if (s.items !== undefined) walk(s.items, `${path}/items`);
  };
  walk(schema, '#');
  return rules;
}

const MAX_ATTEMPTS = 30;

/**
 * 生成"只违反 rule、尽量满足其他所有规则"的反例。
 * 产出交给独立验证器验真：必须至少违反目标规则，且不得违反任何其他规则，
 * 否则重试；重试耗尽或原理上不可构造时返回 { ok: false, reason }，不伪造。
 */
export function generateCounterexample(
  schema: Schema,
  rule: RuleRef,
  seed = 1,
): CounterexampleResult {
  assertSupported(schema);
  // 矛盾 schema 无正例，直接说明，不进入构造流程
  const sanity = generate(schema, seed);
  if (!sanity.ok) {
    return { ok: false, reason: `schema 本身矛盾（不存在正例）：${sanity.reason}` };
  }
  const nodePath = rule.schemaPath.slice(0, rule.schemaPath.length - rule.keyword.length - 1);
  const segments = nodePath === '#' ? [] : nodePath.slice(2).split('/');
  const rng = new Rng(seed);
  let lastReason = '生成结果总会误伤其他规则';

  for (let attempt = 0; attempt < MAX_ATTEMPTS; attempt++) {
    let instance: unknown;
    try {
      instance = buildAt(schema, segments, rule.keyword, rng, '#');
    } catch (e) {
      if (e instanceof CannotConstruct) return { ok: false, reason: e.message };
      if (e instanceof Unsatisfiable) {
        return { ok: false, reason: `schema 本身矛盾：${e.message}` };
      }
      throw e;
    }
    const errors = validate(instance, schema);
    const onTarget = (e: ValidationError) =>
      e.keyword === rule.keyword && e.schemaPath === rule.schemaPath;
    const targetErrors = errors.filter(onTarget);
    const otherErrors = errors.filter((e) => !onTarget(e));
    if (targetErrors.length > 0 && otherErrors.length === 0) {
      return { ok: true, instance, violations: targetErrors };
    }
    if (otherErrors.length > 0) {
      lastReason =
        `无法在不违反其他规则的前提下构造：误伤 ` +
        otherErrors.map((e) => `${e.schemaPath}`).join(', ');
    }
  }
  return { ok: false, reason: lastReason };
}

/** 沿规则路径下潜，在目标节点构造违规值，沿途节点保持合法。 */
function buildAt(
  schema: Schema,
  segments: string[],
  keyword: string,
  rng: Rng,
  path: string,
): unknown {
  if (segments.length === 0) return violateNode(schema, keyword, rng, path);
  const [head, ...rest] = segments;

  if (head === 'properties') {
    const key = unescapePointer(rest[0]);
    const child = schema.properties?.[key];
    if (!child) throw new CannotConstruct(`${path}: 规则路径中的属性 "${key}" 不存在`);
    const childValue = buildAt(child, rest.slice(1), keyword, rng, `${path}/properties/${rest[0]}`);
    return genObject(schema, rng, path, {
      force: new Set([key]),
      override: new Map([[key, childValue]]),
    });
  }

  if (head === 'items') {
    const child = schema.items;
    if (!child) throw new CannotConstruct(`${path}: 规则路径指向 items，但 schema 无 items`);
    if ((schema.maxItems ?? Infinity) < 1) {
      throw new CannotConstruct(`${path}/items: maxItems=0，数组必为空，items 规则不可达`);
    }
    const childValue = buildAt(child, rest, keyword, rng, `${path}/items`);
    const minI = Math.max(schema.minItems ?? 0, 1);
    const maxI = schema.maxItems ?? minI + 3;
    const n = rng.int(minI, Math.max(minI, maxI));
    const arr: unknown[] = [childValue];
    for (let i = 1; i < n; i++) arr.push(genNode(child, rng, `${path}/items`));
    return arr;
  }

  throw new CannotConstruct(`${path}: 不支持的规则路径段 "${head}"`);
}

/** 在目标节点构造"违反 keyword、但满足同节点其余约束"的值。 */
function violateNode(schema: Schema, keyword: string, rng: Rng, path: string): unknown {
  const rest: Schema = { ...schema };
  delete (rest as Record<string, unknown>)[keyword];

  /** 候选中第一个满足其余约束的值。 */
  const firstValid = (candidates: unknown[]): unknown | null => {
    for (const c of candidates) {
      if (validate(c, rest).length === 0) return c;
    }
    return null;
  };

  switch (keyword) {
    case 'type': {
      const candidates: unknown[] = ['x', 1.5, true, null, [], {}];
      for (const c of candidates) {
        if (validate(c, { type: schema.type }).length === 0) continue; // 没违反 type
        if (validate(c, rest).length === 0) return c;
      }
      throw new CannotConstruct(
        `${path}: 违反 type 必然同时违反同节点的 enum/const 等约束，放弃构造`,
      );
    }

    case 'const': {
      for (let i = 0; i < 50; i++) {
        const v = genNode(rest, rng, path);
        if (!deepEqual(v, schema.const)) return v;
      }
      throw new CannotConstruct(`${path}: 其余约束下的取值恒等于 const，无法违反`);
    }

    case 'enum': {
      for (let i = 0; i < 50; i++) {
        const v = genNode(rest, rng, path);
        if (!schema.enum!.some((e) => deepEqual(e, v))) return v;
      }
      throw new CannotConstruct(
        `${path}: 其余约束下的取值均被 enum 覆盖（取值域太小），无法只违反 enum`,
      );
    }

    case 'minimum':
    case 'exclusiveMinimum': {
      const bound = keyword === 'minimum' ? schema.minimum! : schema.exclusiveMinimum!;
      const m = schema.multipleOf;
      const candidates: unknown[] = [];
      if (m !== undefined) {
        // 严格小于 bound 的最大 m 倍数（exclusiveMinimum 允许等于 bound）
        const kMax = Math.ceil(bound / m - 1e-9) - (keyword === 'minimum' ? 1 : 0);
        candidates.push(cleanupNum(kMax * m), cleanupNum((kMax - 1) * m));
      }
      if (schema.type === 'integer') {
        candidates.push(Math.ceil(bound) - 1, Math.ceil(bound) - 2);
      } else {
        candidates.push(bound - 0.5, bound - 1, bound - 100);
      }
      if (keyword === 'exclusiveMinimum') candidates.unshift(bound);
      const v = firstValid(candidates);
      if (v !== null) return v;
      throw new CannotConstruct(
        `${path}: 违反 ${keyword}=${bound} 与其他约束（如下界/倍数）冲突`,
      );
    }

    case 'maximum':
    case 'exclusiveMaximum': {
      const bound = keyword === 'maximum' ? schema.maximum! : schema.exclusiveMaximum!;
      const m = schema.multipleOf;
      const candidates: unknown[] = [];
      if (m !== undefined) {
        const kMin = Math.floor(bound / m + 1e-9) + (keyword === 'maximum' ? 1 : 0);
        candidates.push(cleanupNum(kMin * m), cleanupNum((kMin + 1) * m));
      }
      if (schema.type === 'integer') {
        candidates.push(Math.floor(bound) + 1, Math.floor(bound) + 2);
      } else {
        candidates.push(bound + 0.5, bound + 1, bound + 100);
      }
      if (keyword === 'exclusiveMaximum') candidates.unshift(bound);
      const v = firstValid(candidates);
      if (v !== null) return v;
      throw new CannotConstruct(
        `${path}: 违反 ${keyword}=${bound} 与其他约束（如上界/倍数）冲突`,
      );
    }

    case 'multipleOf': {
      for (let i = 0; i < 30; i++) {
        const v = genNode(rest, rng, path);
        if (typeof v === 'number') {
          const q = v / schema.multipleOf!;
          if (Math.abs(q - Math.round(q)) > 1e-9) return v;
        }
      }
      throw new CannotConstruct(
        `${path}: 其余约束下不存在非 ${schema.multipleOf} 倍数的取值` +
          `（例如 integer 且 multipleOf=1 时原理上不可违反）`,
      );
    }

    case 'minLength': {
      const len = schema.minLength! - 1;
      if (len < 0) {
        throw new CannotConstruct(`${path}: minLength=0 原理上不可违反（空串也合法）`);
      }
      if (schema.maxLength !== undefined && len > schema.maxLength) {
        throw new CannotConstruct(
          `${path}: 违反 minLength 需要长度 ${len}，但 maxLength=${schema.maxLength} 更严（schema 本身矛盾）`,
        );
      }
      if (schema.pattern !== undefined) {
        const ast = parsePattern(schema.pattern);
        if (ast.minLength > len) {
          throw new CannotConstruct(
            `${path}: pattern 要求长度至少 ${ast.minLength}，与"长度仅 ${len}"冲突`,
          );
        }
        const s = generateFromPattern(ast, rng, len, len);
        if (s !== null) return s;
        throw new CannotConstruct(`${path}: 无法生成满足 pattern 且长度为 ${len} 的字符串`);
      }
      return randomString(rng, len);
    }

    case 'maxLength': {
      const len = schema.maxLength! + 1;
      if (schema.pattern !== undefined) {
        const ast = parsePattern(schema.pattern);
        if (ast.maxLength < len) {
          throw new CannotConstruct(
            `${path}: pattern 最多允许长度 ${ast.maxLength}，无法达到长度 ${len}`,
          );
        }
        const s = generateFromPattern(ast, rng, len, len);
        if (s !== null) return s;
        throw new CannotConstruct(`${path}: 无法生成满足 pattern 且长度为 ${len} 的字符串`);
      }
      return randomString(rng, len);
    }

    case 'pattern': {
      const regex = new RegExp(schema.pattern!);
      for (let i = 0; i < 200; i++) {
        const s = genString(rest, rng, path);
        if (!regex.test(s)) return s;
      }
      throw new CannotConstruct(`${path}: 长度约束内找不到不匹配该 pattern 的字符串`);
    }

    case 'minItems': {
      const n = schema.minItems! - 1;
      if (n < 0) {
        throw new CannotConstruct(`${path}: minItems=0 原理上不可违反（空数组也合法）`);
      }
      if (schema.maxItems !== undefined && n > schema.maxItems) {
        throw new CannotConstruct(
          `${path}: 违反 minItems 需要长度 ${n}，但 maxItems=${schema.maxItems} 更严（schema 本身矛盾）`,
        );
      }
      return genArrayOfSize(schema, n, rng, path);
    }

    case 'maxItems': {
      const n = schema.maxItems! + 1;
      return genArrayOfSize(schema, n, rng, path);
    }

    case 'uniqueItems': {
      const n = Math.max(2, schema.minItems ?? 0);
      if ((schema.maxItems ?? Infinity) < n) {
        throw new CannotConstruct(
          `${path}: maxItems=${schema.maxItems} < 2，数组容不下重复元素，无法违反 uniqueItems`,
        );
      }
      const item = genNode(schema.items ?? {}, rng, `${path}/items`);
      const arr: unknown[] = [item, item];
      while (arr.length < n) arr.push(genNode(schema.items ?? {}, rng, `${path}/items`));
      return arr;
    }

    case 'required': {
      const required = schema.required ?? [];
      const deps = schema.dependentRequired ?? {};
      for (const key of required) {
        const triggers = Object.entries(deps)
          .filter(([, d]) => d.includes(key))
          .map(([t]) => t);
        if (triggers.some((t) => required.includes(t))) continue; // 联动牵连，换目标
        return genObject(schema, rng, path, { omit: new Set([key, ...triggers]) });
      }
      throw new CannotConstruct(
        `${path}: 每个 required 属性都被其他 required 属性的 dependentRequired 联动牵连，` +
          `省略它会连带违反 dependentRequired，无法只违反 required`,
      );
    }

    case 'additionalProperties': {
      const obj = genObject(schema, rng, path);
      let extra = 'extraProp';
      let i = 0;
      const props = schema.properties ?? {};
      while (extra in props || extra in obj) extra = `extraProp${++i}`;
      obj[extra] = genDefaultValue(rng);
      return obj;
    }

    case 'dependentRequired': {
      const required = schema.required ?? [];
      const entries = Object.entries(schema.dependentRequired ?? {});
      for (const [trigger, dependents] of entries) {
        for (const dep of dependents) {
          if (required.includes(dep)) continue; // 省略 dep 会连带违反 required
          const otherTriggers = entries
            .filter(([t, d]) => t !== trigger && d.includes(dep))
            .map(([t]) => t);
          if (otherTriggers.some((t) => required.includes(t))) continue;
          return genObject(schema, rng, path, {
            force: new Set([trigger]),
            omit: new Set([dep, ...otherTriggers]),
          });
        }
      }
      throw new CannotConstruct(
        `${path}: 依赖属性同时被 required 或其他联动规则保护，无法只违反 dependentRequired`,
      );
    }

    default:
      throw new CannotConstruct(`${path}: 未实现针对 "${keyword}" 的反例构造`);
  }
}

function genArrayOfSize(schema: Schema, n: number, rng: Rng, path: string): unknown[] {
  if (schema.uniqueItems) {
    const dom = domainSize(schema.items);
    if (dom < n) {
      throw new CannotConstruct(
        `${path}: uniqueItems 下元素取值域大小仅 ${dom}，凑不出 ${n} 个互异元素`,
      );
    }
  }
  const arr: unknown[] = [];
  for (let i = 0; i < n; i++) {
    if (!schema.uniqueItems) {
      arr.push(genNode(schema.items ?? {}, rng, `${path}/items`));
      continue;
    }
    let placed = false;
    for (let attempt = 0; attempt < 100 && !placed; attempt++) {
      const c = genNode(schema.items ?? {}, rng, `${path}/items`);
      if (!arr.some((e) => deepEqual(e, c))) {
        arr.push(c);
        placed = true;
      }
    }
    if (!placed) {
      throw new CannotConstruct(`${path}: 无法凑出 ${n} 个互异元素（取值域过小）`);
    }
  }
  return arr;
}

function randomString(rng: Rng, len: number): string {
  let out = '';
  for (let i = 0; i < len; i++) out += ALPHABET[rng.int(0, ALPHABET.length - 1)];
  return out;
}

function cleanupNum(n: number): number {
  return Number(n.toPrecision(12));
}
