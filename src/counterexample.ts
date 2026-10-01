import type { Schema } from "./schema.ts";
import { makeRng, randInt, pick, type Rng } from "./rng.ts";
import { validate, deepEqual, matchesType, type ValidationError } from "./validator.ts";
import { generate, without, type GenResult } from "./generator.ts";
import { parsePattern, samplePattern } from "./regex-lite.ts";

/**
 * Counterexample engine: for every rule in the schema it tries to build
 * an instance that violates ONLY that rule while still satisfying every
 * other rule. Every emitted counterexample is re-checked with the
 * independent validator; if the violation cannot be isolated, a reason
 * is reported instead of a fabricated instance.
 */
export interface RuleTarget {
  keyword: string;
  schemaPath: string;
  instancePath: string;
  node: Schema;
}

export type CounterexampleResult =
  | { ok: true; target: RuleTarget; instance: unknown; error: ValidationError }
  | { ok: false; target: RuleTarget; reason: string };

const KEYWORDS = [
  "const",
  "enum",
  "type",
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "multipleOf",
  "minLength",
  "maxLength",
  "pattern",
  "minItems",
  "maxItems",
  "required",
  "additionalProperties",
] as const;

export function listRules(schema: Schema): RuleTarget[] {
  const targets: RuleTarget[] = [];
  const walk = (node: Schema, schemaPath: string, instancePath: string) => {
    for (const kw of KEYWORDS) {
      if (kw === "required" && (!node.required || node.required.length === 0)) continue;
      if (kw === "additionalProperties" && node.additionalProperties !== false) continue;
      if ((node as Record<string, unknown>)[kw] !== undefined)
        targets.push({ keyword: kw, schemaPath: `${schemaPath}/${kw}`, instancePath, node });
    }
    if (node.properties !== undefined)
      for (const [key, sub] of Object.entries(node.properties))
        walk(sub, `${schemaPath}/properties/${key}`, `${instancePath}/${key}`);
    if (node.items !== undefined) walk(node.items, `${schemaPath}/items`, `${instancePath}/0`);
  };
  walk(schema, "", "");
  return targets;
}

export function allCounterexamples(schema: Schema, seed: number): CounterexampleResult[] {
  const rng = makeRng(seed);
  return listRules(schema).map((target) => counterexampleFor(schema, target, rng));
}

export function counterexampleFor(schema: Schema, target: RuleTarget, rng: Rng): CounterexampleResult {
  const positive = generate(schema, rng);
  if (!positive.ok)
    return { ok: false, target, reason: `schema is unsatisfiable (${positive.reason}); no counterexamples exist` };

  let lastNote = "no candidate produced";
  for (let attempt = 0; attempt < 8; attempt++) {
    const local = violateNode(target.node, target.keyword, rng);
    if (!local.ok) return { ok: false, target, reason: local.reason };
    const instance = setAtPath(positive.value, target.instancePath, local.value);
    const errors = validate(instance, schema);
    if (errors.length === 1 && errors[0].schemaPath === target.schemaPath)
      return { ok: true, target, instance, error: errors[0] };
    lastNote = `verification showed ${errors.length} error(s) at [${errors.map((e) => e.schemaPath).join(", ")}]`;
  }
  return {
    ok: false,
    target,
    reason: `could not isolate a violation of ${target.schemaPath}; ${lastNote}`,
  };
}

const ok = (value: unknown): GenResult => ({ ok: true, value });
const fail = (reason: string): GenResult => ({ ok: false, reason });

function setAtPath(value: unknown, path: string, sub: unknown): unknown {
  if (path === "") return sub;
  const segments = path.slice(1).split("/");
  const head = segments[0];
  const rest = segments.slice(1).join("/");
  const childPath = rest === "" ? "" : `/${rest}`;
  if (Array.isArray(value)) {
    const copy = value.slice();
    const idx = Number(head);
    copy[idx] = setAtPath(copy[idx], childPath, sub);
    return copy;
  }
  const copy = { ...(value as Record<string, unknown>) };
  copy[head] = setAtPath(copy[head], childPath, sub);
  return copy;
}

/** Local check: candidate must produce exactly one error, at this keyword. */
function firstIsolated(candidates: unknown[], node: Schema, keyword: string): GenResult {
  for (const c of candidates) {
    const errors = validate(c, node);
    if (errors.length === 1 && errors[0].schemaPath === `/${keyword}`) return ok(c);
  }
  return fail(
    `no value violates "${keyword}" while satisfying the other constraints of this node ` +
      `(they are linked too tightly)`,
  );
}

function violateNode(node: Schema, keyword: string, rng: Rng): GenResult {
  switch (keyword) {
    case "type": {
      if (node.const !== undefined)
        return fail(`const ${JSON.stringify(node.const)} pins the value, so "type" cannot be violated alone`);
      if (node.enum !== undefined)
        return fail(`enum pins the allowed values, so "type" cannot be violated alone`);
      const candidates: unknown[] = ["text", true, null, [0], { k: 0 }];
      const viable = candidates.filter(
        (c) => !matchesType(c, node.type as string) && validate(c, without(node, "type")).length === 0,
      );
      if (viable.length > 0) return ok(pick(rng, viable));
      return fail(`every alternative JSON type violates another constraint of this node`);
    }

    case "const":
    case "enum": {
      const rest = without(node, keyword);
      const candidates: unknown[] = [];
      for (let i = 0; i < 30; i++) {
        const r = generate(rest, rng);
        if (!r.ok) return fail(`cannot build a value for the schema without "${keyword}": ${r.reason}`);
        const differs =
          keyword === "const"
            ? !deepEqual(r.value, node.const)
            : !(node.enum as unknown[]).some((e) => deepEqual(e, r.value));
        if (differs) candidates.push(r.value);
      }
      if (candidates.length === 0)
        return fail(
          `every value satisfying the remaining constraints is fixed by "${keyword}"; the violation cannot be isolated`,
        );
      return firstIsolated(candidates, node, keyword);
    }

    case "minimum":
    case "maximum":
    case "exclusiveMinimum":
    case "exclusiveMaximum":
    case "multipleOf":
      return violateNumeric(node, keyword, rng);

    case "minLength":
    case "maxLength":
    case "pattern":
      return violateString(node, keyword, rng);

    case "minItems":
    case "maxItems":
      return violateArray(node, keyword, rng);

    case "required": {
      const base = generate(node, rng);
      if (!base.ok) return base;
      const obj = { ...(base.value as Record<string, unknown>) };
      delete obj[(node.required as string[])[0]];
      return firstIsolated([obj], node, keyword);
    }

    case "additionalProperties": {
      const base = generate(node, rng);
      if (!base.ok) return base;
      const obj = { ...(base.value as Record<string, unknown>) };
      obj[`extra_${randInt(rng, 100, 999)}`] = "surprise";
      return firstIsolated([obj], node, keyword);
    }

    default:
      return fail(`no counterexample strategy implemented for keyword "${keyword}"`);
  }
}

const EPS = 1e-9;

function violateNumeric(node: Schema, keyword: string, rng: Rng): GenResult {
  const m = node.multipleOf;
  const integer = node.type === "integer";
  const candidates: number[] = [];
  switch (keyword) {
    case "minimum": {
      const min = node.minimum as number;
      if (m !== undefined) candidates.push((Math.ceil(min / m - EPS) - 1) * m);
      if (integer) candidates.push(Math.ceil(min - EPS) - 1);
      candidates.push(min - 1, min - 0.5);
      break;
    }
    case "maximum": {
      const max = node.maximum as number;
      if (m !== undefined) candidates.push((Math.floor(max / m + EPS) + 1) * m);
      if (integer) candidates.push(Math.floor(max + EPS) + 1);
      candidates.push(max + 1, max + 0.5);
      break;
    }
    case "exclusiveMinimum":
      candidates.push(node.exclusiveMinimum as number, (node.exclusiveMinimum as number) - 1);
      break;
    case "exclusiveMaximum":
      candidates.push(node.exclusiveMaximum as number, (node.exclusiveMaximum as number) + 1);
      break;
    case "multipleOf": {
      const rest = without(node, "multipleOf");
      for (let i = 0; i < 40; i++) {
        const r = generate(rest, rng);
        if (r.ok && typeof r.value === "number") candidates.push(r.value);
      }
      break;
    }
  }
  return firstIsolated(candidates, node, keyword);
}

function violateString(node: Schema, keyword: string, rng: Rng): GenResult {
  const minL = node.minLength ?? 0;
  const maxL = node.maxLength ?? Math.max(minL, 12);
  const alphabet = "abcdefghijklmnopqrstuvwxyz";
  const randomString = (len: number) => {
    let s = "";
    for (let i = 0; i < len; i++) s += alphabet[randInt(rng, 0, alphabet.length - 1)];
    return s;
  };

  if (keyword === "pattern") {
    const re = new RegExp(node.pattern as string);
    const candidates: string[] = [];
    for (let i = 0; i < 60; i++) {
      const s = randomString(randInt(rng, minL, maxL));
      if (!re.test(s)) candidates.push(s);
    }
    if (candidates.length === 0)
      return fail(
        `could not find a string of length ${minL}..${maxL} that does NOT match ${JSON.stringify(node.pattern)}`,
      );
    return firstIsolated(candidates, node, keyword);
  }

  const targetLen =
    keyword === "minLength" ? { min: 0, max: minL - 1 } : { min: maxL + 1, max: maxL + 10 };
  if (keyword === "minLength" && minL === 0)
    return fail(`minLength is 0; no string is shorter, so the rule cannot be violated`);

  if (node.pattern !== undefined) {
    const pieces = parsePattern(node.pattern);
    if (pieces === null)
      return fail(
        `pattern ${JSON.stringify(node.pattern)} is outside the supported generation subset; ` +
          `cannot build a matching string that violates "${keyword}"`,
      );
    const re = new RegExp(node.pattern);
    const candidates: string[] = [];
    for (let i = 0; i < 60; i++) {
      const s = samplePattern(pieces, rng, targetLen);
      if (re.test(s)) candidates.push(s);
    }
    if (candidates.length === 0)
      return fail(
        `the pattern forces lengths that cannot violate "${keyword}" (target length ${targetLen.min}..${targetLen.max})`,
      );
    return firstIsolated(candidates, node, keyword);
  }

  const len = randInt(rng, targetLen.min, targetLen.max);
  return firstIsolated([randomString(len)], node, keyword);
}

function violateArray(node: Schema, keyword: string, rng: Rng): GenResult {
  const minI = node.minItems ?? 0;
  const maxI = node.maxItems ?? Math.max(minI, 1);
  if (keyword === "minItems" && minI === 0)
    return fail(`minItems is 0; no array is shorter, so the rule cannot be violated`);
  const len = keyword === "minItems" ? randInt(rng, 0, minI - 1) : randInt(rng, maxI + 1, maxI + 3);
  const arr: unknown[] = [];
  for (let i = 0; i < len; i++) {
    const r = generate(node.items ?? {}, rng);
    if (!r.ok) return fail(`cannot build valid items for the array: ${r.reason}`);
    arr.push(r.value);
  }
  return firstIsolated([arr], node, keyword);
}
