import { fullSet, subtract, type ValSet } from "./valset.ts";

/** Finite field domain: a listed enum, or a bounded integer interval. */
export type Domain =
  | { kind: "enum"; values: string[] }
  | { kind: "int"; min: number; max: number };

export interface FieldDef {
  name: string;
  domain: Domain;
}

/** Condition JSON accepted per field inside a rule's `when` object. */
export type ConditionJson =
  | string
  | number
  | Array<string | number>
  | {
      any?: true;
      in?: Array<string | number>;
      notIn?: Array<string | number>;
      range?: [number | null, number | null];
      notRange?: [number | null, number | null];
    };

export interface RuleJson {
  name: string;
  priority: number;
  when?: Record<string, ConditionJson>;
}

export type FieldJson =
  | { name: string; type: "enum"; values: string[] }
  | { name: string; type: "int"; min: number; max: number };

export interface TableJson {
  fields: FieldJson[];
  rules: RuleJson[];
}

export interface CompiledRule {
  name: string;
  priority: number;
  /** Per-field allowed value sets; an input matches iff every field is in its set. */
  box: ValSet[];
}

export interface CompiledTable {
  fields: FieldDef[];
  domainSizes: number[];
  rules: CompiledRule[];
}

export function domainSize(domain: Domain): number {
  return domain.kind === "enum" ? domain.values.length : domain.max - domain.min + 1;
}

export function indexToValue(domain: Domain, index: number): string | number {
  return domain.kind === "enum" ? domain.values[index] : domain.min + index;
}

function valueIndex(domain: Domain, value: string | number, ctx: string): number {
  if (domain.kind === "enum") {
    if (typeof value !== "string") {
      throw new Error(`${ctx}: expected a string enum value, got ${JSON.stringify(value)}`);
    }
    const idx = domain.values.indexOf(value);
    if (idx < 0) {
      throw new Error(`${ctx}: unknown enum value ${JSON.stringify(value)} (domain: ${domain.values.join(", ")})`);
    }
    return idx;
  }
  if (typeof value !== "number" || !Number.isInteger(value)) {
    throw new Error(`${ctx}: expected an integer, got ${JSON.stringify(value)}`);
  }
  if (value < domain.min || value > domain.max) {
    throw new Error(`${ctx}: value ${value} outside domain ${domain.min}..${domain.max}`);
  }
  return value - domain.min;
}

function pointSet(domain: Domain, values: Array<string | number>, ctx: string): ValSet {
  const indices = values.map((v) => valueIndex(domain, v, ctx)).sort((a, b) => a - b);
  const out: ValSet = [];
  for (const idx of indices) {
    const last = out[out.length - 1];
    if (last && idx <= last[1] + 1) last[1] = Math.max(last[1], idx);
    else out.push([idx, idx]);
  }
  return out;
}

function rangeSet(
  domain: Domain,
  lo: number | null,
  hi: number | null,
  ctx: string,
): ValSet {
  if (domain.kind !== "int") {
    throw new Error(`${ctx}: range conditions require an int field`);
  }
  for (const [label, v] of [["min", lo], ["max", hi]] as const) {
    if (v !== null && !Number.isInteger(v)) {
      throw new Error(`${ctx}: range ${label} must be an integer or null, got ${JSON.stringify(v)}`);
    }
  }
  const size = domainSize(domain);
  const loIdx = lo === null ? 0 : Math.max(0, lo - domain.min);
  const hiIdx = hi === null ? size - 1 : Math.min(size - 1, hi - domain.min);
  return loIdx > hiIdx ? [] : [[loIdx, hiIdx]];
}

function compileCondition(domain: Domain, raw: ConditionJson | undefined, ctx: string): ValSet {
  const size = domainSize(domain);
  if (raw === undefined) return fullSet(size);
  if (typeof raw === "string" || typeof raw === "number") return pointSet(domain, [raw], ctx);
  if (Array.isArray(raw)) return pointSet(domain, raw, ctx);
  if (typeof raw === "object" && raw !== null) {
    const keys = Object.keys(raw);
    if (keys.length !== 1) {
      throw new Error(`${ctx}: condition object must have exactly one of any/in/notIn/range/notRange, got [${keys}]`);
    }
    if (raw.any === true) return fullSet(size);
    if (raw.in !== undefined) return pointSet(domain, raw.in, ctx);
    if (raw.notIn !== undefined) return subtract(fullSet(size), pointSet(domain, raw.notIn, ctx));
    if (raw.range !== undefined) return rangeSet(domain, raw.range[0] ?? null, raw.range[1] ?? null, ctx);
    if (raw.notRange !== undefined) {
      return subtract(fullSet(size), rangeSet(domain, raw.notRange[0] ?? null, raw.notRange[1] ?? null, ctx));
    }
  }
  throw new Error(`${ctx}: unsupported condition ${JSON.stringify(raw)}`);
}

export function compileTable(json: TableJson): CompiledTable {
  if (!Array.isArray(json.fields) || json.fields.length === 0) {
    throw new Error("table must declare at least one field");
  }
  const fields: FieldDef[] = json.fields.map((f) => {
    if (f.type === "enum") {
      if (!Array.isArray(f.values) || f.values.length === 0) {
        throw new Error(`field ${f.name}: enum domain needs a non-empty values list`);
      }
      if (new Set(f.values).size !== f.values.length) {
        throw new Error(`field ${f.name}: duplicate enum values`);
      }
      return { name: f.name, domain: { kind: "enum", values: f.values.slice() } };
    }
    if (f.type === "int") {
      if (!Number.isInteger(f.min) || !Number.isInteger(f.max) || f.min > f.max) {
        throw new Error(`field ${f.name}: int domain needs integer min <= max`);
      }
      return { name: f.name, domain: { kind: "int", min: f.min, max: f.max } };
    }
    throw new Error(`field ${f.name}: unknown type ${JSON.stringify((f as { type: unknown }).type)}`);
  });
  const fieldIndex = new Map(fields.map((f, i) => [f.name, i]));
  if (fieldIndex.size !== fields.length) throw new Error("duplicate field names");

  const rules: CompiledRule[] = json.rules.map((rule, ri) => {
    const ctx = `rule ${rule.name ?? `#${ri}`}`;
    if (typeof rule.priority !== "number" || !Number.isFinite(rule.priority)) {
      throw new Error(`${ctx}: priority must be a finite number`);
    }
    const when = rule.when ?? {};
    for (const key of Object.keys(when)) {
      if (!fieldIndex.has(key)) throw new Error(`${ctx}: unknown field ${JSON.stringify(key)} in when`);
    }
    const box = fields.map((f) =>
      compileCondition(f.domain, when[f.name], `${ctx}, field ${f.name}`),
    );
    return { name: rule.name ?? `rule#${ri}`, priority: rule.priority, box };
  });

  return { fields, domainSizes: fields.map((f) => domainSize(f.domain)), rules };
}
