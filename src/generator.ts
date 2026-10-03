import { Rng } from './rng.ts';
import { assertSupported, escapePointer, type Schema } from './schema.ts';
import { generateFromPattern, parsePattern, ALPHABET } from './pattern.ts';
import { validate } from './validator.ts';

/** schema 自相矛盾、不存在任何合法实例时抛出（内部使用）。 */
export class Unsatisfiable extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'Unsatisfiable';
  }
}

export type GenResult =
  | { ok: true; value: unknown }
  | { ok: false; reason: string };

/**
 * 生成满足 schema 的正例。矛盾 schema 不伪造结果，返回 { ok: false, reason }。
 * 生成结果会交给独立验证器验真，验不过视为生成器 bug 直接抛错。
 * 相同 seed 输出完全一致。
 */
export function generate(schema: Schema, seed = 1): GenResult {
  assertSupported(schema);
  const rng = new Rng(seed);
  let value: unknown;
  try {
    value = genNode(schema, rng, '#');
  } catch (e) {
    if (e instanceof Unsatisfiable) return { ok: false, reason: e.message };
    throw e;
  }
  const errors = validate(value, schema);
  if (errors.length > 0) {
    throw new Error(
      `生成器内部错误：产出未通过独立验证器：${JSON.stringify(errors, null, 2)}`,
    );
  }
  return { ok: true, value };
}

export function genNode(schema: Schema, rng: Rng, path: string): unknown {
  if ('const' in schema) {
    const rest = { ...schema };
    delete rest.const;
    const errors = validate(schema.const, rest);
    if (errors.length > 0) {
      throw new Unsatisfiable(
        `${path}: const ${JSON.stringify(schema.const)} 与其他约束矛盾（${errors[0].message}）`,
      );
    }
    return schema.const;
  }
  if (schema.enum !== undefined) {
    const rest = { ...schema };
    delete rest.enum;
    const candidates = schema.enum.filter((v) => validate(v, rest).length === 0);
    if (candidates.length === 0) {
      throw new Unsatisfiable(
        `${path}: enum 中没有任何值同时满足其余约束（如类型/范围/长度）`,
      );
    }
    return rng.pick(candidates);
  }
  switch (schema.type) {
    case 'string':
      return genString(schema, rng, path);
    case 'integer':
      return genNumber(schema, rng, path, true);
    case 'number':
      return genNumber(schema, rng, path, false);
    case 'boolean':
      return rng.bool();
    case 'null':
      return null;
    case 'array':
      return genArray(schema, rng, path);
    case 'object':
      return genObject(schema, rng, path);
    case undefined:
      return genDefaultValue(rng); // 无 type 且无 enum/const：任意 JSON 值
  }
}

function genString(schema: Schema, rng: Rng, path: string): string {
  const minL = schema.minLength ?? 0;
  const maxL = schema.maxLength ?? minL + 8;
  if (minL > maxL) {
    throw new Unsatisfiable(`${path}: minLength(${minL}) > maxLength(${maxL})，长度区间为空`);
  }
  if (schema.pattern !== undefined) {
    const ast = parsePattern(schema.pattern);
    const s = generateFromPattern(ast, rng, minL, maxL);
    if (s === null) {
      throw new Unsatisfiable(
        `${path}: 长度约束 [${minL}, ${maxL}] 与 pattern 可达长度 ` +
          `[${ast.minLength}, ${ast.maxLength === Infinity ? '∞' : ast.maxLength}] 无交集`,
      );
    }
    return s;
  }
  const len = rng.int(minL, maxL);
  let out = '';
  for (let i = 0; i < len; i++) out += ALPHABET[rng.int(0, ALPHABET.length - 1)];
  return out;
}

const BOUND_LIMIT = 1000;

function genNumber(schema: Schema, rng: Rng, path: string, integer: boolean): number {
  let lo = schema.minimum ?? -Infinity;
  let hi = schema.maximum ?? Infinity;
  let loExclusive = false;
  let hiExclusive = false;
  if (schema.exclusiveMinimum !== undefined && schema.exclusiveMinimum >= lo) {
    lo = schema.exclusiveMinimum;
    loExclusive = true;
  }
  if (schema.exclusiveMaximum !== undefined && schema.exclusiveMaximum <= hi) {
    hi = schema.exclusiveMaximum;
    hiExclusive = true;
  }
  const m = schema.multipleOf;

  if (integer) {
    const ilo = loExclusive ? Math.floor(lo) + 1 : Math.ceil(lo);
    const ihi = hiExclusive ? Math.ceil(hi) - 1 : Math.floor(hi);
    if (ilo > ihi) {
      throw new Unsatisfiable(`${path}: 整数区间 [${ilo}, ${ihi}] 为空`);
    }
    if (m !== undefined) {
      const kLo = Math.ceil(ilo / m - 1e-9);
      const kHi = Math.floor(ihi / m + 1e-9);
      if (kLo > kHi) {
        throw new Unsatisfiable(`${path}: [${ilo}, ${ihi}] 内不存在 ${m} 的整数倍`);
      }
      if (Number.isInteger(m)) {
        return pickClamped(rng, kLo, kHi) * m;
      }
      // 非整数 multipleOf：仅部分 k 使 k*m 为整数，确定性扫描候选
      const valid: number[] = [];
      for (let k = kLo; k <= Math.min(kHi, kLo + 500) && valid.length < 200; k++) {
        if (Number.isInteger(cleanup(k * m))) valid.push(k);
      }
      if (valid.length === 0) {
        throw new Unsatisfiable(`${path}: [${ilo}, ${ihi}] 内找不到 ${m} 的整数倍`);
      }
      return rng.pick(valid) * m;
    }
    return pickClamped(rng, ilo, ihi);
  }

  if (lo > hi || (lo === hi && (loExclusive || hiExclusive))) {
    throw new Unsatisfiable(`${path}: 数值区间 (${lo}, ${hi}) 为空`);
  }
  const effLo = lo === -Infinity ? (hi === Infinity ? -BOUND_LIMIT : hi - 2 * BOUND_LIMIT) : lo;
  const effHi = hi === Infinity ? effLo + 2 * BOUND_LIMIT : hi;
  if (m !== undefined) {
    const kLo = loExclusive ? Math.floor(lo / m + 1e-9) + 1 : Math.ceil(lo / m - 1e-9);
    const kHi = hiExclusive ? Math.ceil(hi / m - 1e-9) - 1 : Math.floor(hi / m + 1e-9);
    if (kLo > kHi) {
      throw new Unsatisfiable(`${path}: 区间 (${lo}, ${hi}) 内不存在 ${m} 的倍数`);
    }
    return cleanup(pickClamped(rng, kLo, kHi) * m);
  }
  const raw = effLo + rng.float() * (effHi - effLo);
  const v = cleanup(raw);
  // 浮点取整可能触碰边界，夹回开区间内部
  if (loExclusive && v <= lo) return cleanup(lo + (effHi - lo) / 2);
  if (hiExclusive && v >= hi) return cleanup(hi - (hi - effLo) / 2);
  return v;
}

function cleanup(n: number): number {
  return Number(n.toPrecision(12));
}

/** 在 [lo, hi] 内取随机整数；区间过宽时收缩到安全宽度，避免浮点精度问题。 */
function pickClamped(rng: Rng, lo: number, hi: number): number {
  const SAFE = 1e12;
  const lo2 = Math.max(lo, -SAFE);
  const hi2 = Math.min(hi, SAFE);
  if (lo2 > hi2) throw new Unsatisfiable(`区间 [${lo}, ${hi}] 超出可安全生成范围`);
  const upper = Math.min(hi2, lo2 + 2 * BOUND_LIMIT);
  return rng.int(lo2, upper);
}

/** 有限取值域大小估计（用于 uniqueItems 可行性判断），无限域返回 Infinity。 */
export function domainSize(schema: Schema | undefined): number {
  if (!schema) return Infinity;
  if ('const' in schema) return 1;
  if (schema.enum !== undefined) return schema.enum.length;
  switch (schema.type) {
    case 'boolean':
      return 2;
    case 'null':
      return 1;
    case 'integer': {
      const lo = schema.minimum ?? schema.exclusiveMinimum;
      const hi = schema.maximum ?? schema.exclusiveMaximum;
      if (lo === undefined || hi === undefined) return Infinity;
      const ilo = schema.exclusiveMinimum !== undefined && schema.minimum === undefined
        ? Math.floor(lo) + 1
        : Math.ceil(lo);
      const ihi = schema.exclusiveMaximum !== undefined && schema.maximum === undefined
        ? Math.ceil(hi) - 1
        : Math.floor(hi);
      return Math.max(0, ihi - ilo + 1);
    }
    default:
      return Infinity;
  }
}

function genArray(schema: Schema, rng: Rng, path: string): unknown[] {
  const minI = schema.minItems ?? 0;
  const maxI = schema.maxItems ?? minI + 3;
  if (minI > maxI) {
    throw new Unsatisfiable(`${path}: minItems(${minI}) > maxItems(${maxI})，长度区间为空`);
  }
  let upper = maxI;
  if (schema.uniqueItems) {
    const dom = domainSize(schema.items);
    upper = Math.min(maxI, dom);
    if (minI > upper) {
      throw new Unsatisfiable(
        `${path}: uniqueItems 要求 ${minI} 个互异元素，但元素取值域大小仅 ${dom}`,
      );
    }
  }
  const n = rng.int(minI, upper);
  const out: unknown[] = [];
  for (let i = 0; i < n; i++) {
    if (!schema.uniqueItems) {
      out.push(genNode(schema.items ?? {}, rng, `${path}/items`));
      continue;
    }
    let placed = false;
    for (let attempt = 0; attempt < 100 && !placed; attempt++) {
      const candidate = genNode(schema.items ?? {}, rng, `${path}/items`);
      if (!out.some((existing) => uniqueEq(existing, candidate))) {
        out.push(candidate);
        placed = true;
      }
    }
    if (!placed) {
      throw new Unsatisfiable(`${path}: 无法生成 ${n} 个互不相同的元素（取值域过小）`);
    }
  }
  return out;
}

function uniqueEq(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export interface ObjectGenOptions {
  /** 强制出现的属性（即使非 required）。 */
  force?: ReadonlySet<string>;
  /** 强制省略的属性（用于反例构造）。 */
  omit?: ReadonlySet<string>;
  /** 已构造好的属性值，直接使用。 */
  override?: ReadonlyMap<string, unknown>;
}

function genObject(
  schema: Schema,
  rng: Rng,
  path: string,
  opts: ObjectGenOptions = {},
): Record<string, unknown> {
  const props = schema.properties ?? {};
  const required = schema.required ?? [];
  if (schema.additionalProperties === false) {
    for (const key of required) {
      if (!(key in props)) {
        throw new Unsatisfiable(
          `${path}: required 的 "${key}" 未在 properties 中声明，且 additionalProperties=false，无法构造`,
        );
      }
    }
  }
  const present = new Set<string>();
  for (const key of required) present.add(key);
  for (const key of Object.keys(props)) {
    if (!present.has(key) && rng.bool(0.6)) present.add(key);
  }
  for (const key of opts.force ?? []) present.add(key);

  // dependentRequired 联动：触发属性出现 => 依赖属性必须补齐，迭代到不动点
  const deps = schema.dependentRequired ?? {};
  let changed = true;
  while (changed) {
    changed = false;
    for (const [trigger, dependents] of Object.entries(deps)) {
      if (!present.has(trigger)) continue;
      for (const dep of dependents) {
        if (!present.has(dep)) {
          if (schema.additionalProperties === false && !(dep in props)) {
            throw new Unsatisfiable(
              `${path}: dependentRequired 要求 "${trigger}" 携带 "${dep}"，` +
                `但 "${dep}" 未在 properties 中声明且 additionalProperties=false`,
            );
          }
          present.add(dep);
          changed = true;
        }
      }
    }
  }
  for (const key of opts.omit ?? []) present.delete(key);

  const obj: Record<string, unknown> = {};
  for (const key of present) {
    if (opts.override?.has(key)) {
      obj[key] = opts.override.get(key);
      continue;
    }
    const sub = props[key];
    obj[key] = sub
      ? genNode(sub, rng, `${path}/properties/${escapePointer(key)}`)
      : genDefaultValue(rng);
  }
  return obj;
}

function genDefaultValue(rng: Rng): unknown {
  const kind = rng.int(0, 3);
  switch (kind) {
    case 0:
      return rng.int(-100, 100);
    case 1: {
      const len = rng.int(0, 6);
      let s = '';
      for (let i = 0; i < len; i++) s += ALPHABET[rng.int(0, ALPHABET.length - 1)];
      return s;
    }
    case 2:
      return rng.bool();
    default:
      return null;
  }
}

export { genObject, genString, genNumber, genArray, genDefaultValue };
