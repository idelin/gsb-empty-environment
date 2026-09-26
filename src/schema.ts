/**
 * The supported schema subset — and only this subset. Anything else
 * (unknown kinds, unknown keywords) is rejected with an
 * "unsupported-feature" issue instead of being silently ignored.
 *
 *   { kind: "int", min?, max? }
 *   { kind: "bool" }
 *   { kind: "string" }
 *   { kind: "list", items }
 *   { kind: "object", fields, required? }
 */

import { formatPath } from "./paths.ts";
import { makeIssue, type Issue } from "./issues.ts";
import { typeOf } from "./values.ts";

export type Schema =
  | { readonly kind: "int"; readonly min?: number; readonly max?: number }
  | { readonly kind: "bool" }
  | { readonly kind: "string" }
  | { readonly kind: "list"; readonly items: Schema }
  | {
      readonly kind: "object";
      readonly fields: Readonly<Record<string, Schema>>;
      readonly required: readonly string[];
    };

export type CompileResult<T> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly issues: Issue[] };

const ALLOWED_KEYS: Record<string, readonly string[]> = {
  int: ["kind", "min", "max"],
  bool: ["kind"],
  string: ["kind"],
  list: ["kind", "items"],
  object: ["kind", "fields", "required"],
};

export function compileSchema(raw: unknown): CompileResult<Schema> {
  const issues: Issue[] = [];
  const schema = compileNode(raw, ["schema"], issues);
  if (schema === null || issues.length > 0) return { ok: false, issues };
  return { ok: true, value: schema };
}

function compileNode(raw: unknown, rp: (string | number)[], issues: Issue[]): Schema | null {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    issues.push(
      makeIssue({ kind: "invalid-schema", rulePath: formatPath(rp), message: "schema node must be a plain object" }),
    );
    return null;
  }
  const rec = raw as Record<string, unknown>;
  const kind = rec.kind;
  if (typeof kind !== "string" || !(kind in ALLOWED_KEYS)) {
    issues.push(
      makeIssue({
        kind: "invalid-schema",
        rulePath: formatPath([...rp, "kind"]),
        message: `unknown schema kind: ${String(kind)}`,
      }),
    );
    return null;
  }
  for (const key of Object.keys(rec)) {
    if (!ALLOWED_KEYS[kind]!.includes(key)) {
      issues.push(
        makeIssue({
          kind: "unsupported-feature",
          rulePath: formatPath([...rp, key]),
          message: `keyword "${key}" is outside the supported schema subset; it is rejected, not ignored`,
        }),
      );
    }
  }
  switch (kind) {
    case "int": {
      const min = rec.min;
      const max = rec.max;
      if (min !== undefined && !Number.isInteger(min)) {
        issues.push(
          makeIssue({ kind: "invalid-schema", rulePath: formatPath([...rp, "min"]), message: "min must be an integer" }),
        );
        return null;
      }
      if (max !== undefined && !Number.isInteger(max)) {
        issues.push(
          makeIssue({ kind: "invalid-schema", rulePath: formatPath([...rp, "max"]), message: "max must be an integer" }),
        );
        return null;
      }
      if (typeof min === "number" && typeof max === "number" && min > max) {
        issues.push(
          makeIssue({
            kind: "invalid-schema",
            rulePath: formatPath([...rp, "min"]),
            message: `min (${min}) is greater than max (${max})`,
            related: [{ rulePath: formatPath([...rp, "max"]), note: `max = ${max}` }],
          }),
        );
        return null;
      }
      return {
        kind: "int",
        ...(typeof min === "number" ? { min } : {}),
        ...(typeof max === "number" ? { max } : {}),
      };
    }
    case "bool":
      return { kind: "bool" };
    case "string":
      return { kind: "string" };
    case "list": {
      if (!("items" in rec)) {
        issues.push(
          makeIssue({ kind: "invalid-schema", rulePath: formatPath(rp), message: 'list schema requires "items"' }),
        );
        return null;
      }
      const items = compileNode(rec.items, [...rp, "items"], issues);
      return items === null ? null : { kind: "list", items };
    }
    case "object": {
      const rawFields = rec.fields;
      if (typeof rawFields !== "object" || rawFields === null || Array.isArray(rawFields)) {
        issues.push(
          makeIssue({ kind: "invalid-schema", rulePath: formatPath(rp), message: 'object schema requires "fields"' }),
        );
        return null;
      }
      const fields: Record<string, Schema> = {};
      let failed = false;
      for (const [name, sub] of Object.entries(rawFields as Record<string, unknown>)) {
        const compiled = compileNode(sub, [...rp, "fields", name], issues);
        if (compiled === null) failed = true;
        else fields[name] = compiled;
      }
      const required: string[] = [];
      if (rec.required !== undefined) {
        if (!Array.isArray(rec.required) || rec.required.some((entry) => typeof entry !== "string")) {
          issues.push(
            makeIssue({
              kind: "invalid-schema",
              rulePath: formatPath([...rp, "required"]),
              message: "required must be an array of field names",
            }),
          );
          failed = true;
        } else {
          for (const name of rec.required as string[]) {
            if (!Object.prototype.hasOwnProperty.call(rawFields, name)) {
              issues.push(
                makeIssue({
                  kind: "invalid-schema",
                  rulePath: formatPath([...rp, "required"]),
                  message: `required field "${name}" is not declared in fields`,
                  related: [{ rulePath: formatPath([...rp, "fields"]), note: "declared fields" }],
                }),
              );
              failed = true;
            } else {
              required.push(name);
            }
          }
        }
      }
      return failed ? null : { kind: "object", fields, required };
    }
  }
  return null;
}

/** Phase 1: type and existence checking. Runs before any relation is evaluated. */
export function typeCheck(instance: unknown, schema: Schema): Issue[] {
  const issues: Issue[] = [];
  checkNode(instance, schema, [], ["schema"], issues);
  return issues;
}

function checkNode(
  value: unknown,
  schema: Schema,
  ip: (string | number)[],
  rp: (string | number)[],
  out: Issue[],
): void {
  switch (schema.kind) {
    case "int": {
      const tag = typeOf(value);
      if (tag !== "int") {
        out.push(
          makeIssue({
            kind: "type",
            instancePath: formatPath(ip),
            rulePath: formatPath(rp),
            message: `expected int, got ${tag}`,
          }),
        );
        return;
      }
      const n = value as number;
      if (schema.min !== undefined && n < schema.min) {
        out.push(
          makeIssue({
            kind: "range",
            instancePath: formatPath(ip),
            rulePath: formatPath([...rp, "min"]),
            message: `value ${n} is below min ${schema.min}`,
          }),
        );
      }
      if (schema.max !== undefined && n > schema.max) {
        out.push(
          makeIssue({
            kind: "range",
            instancePath: formatPath(ip),
            rulePath: formatPath([...rp, "max"]),
            message: `value ${n} is above max ${schema.max}`,
          }),
        );
      }
      return;
    }
    case "bool":
    case "string": {
      const tag = typeOf(value);
      if (tag !== schema.kind) {
        out.push(
          makeIssue({
            kind: "type",
            instancePath: formatPath(ip),
            rulePath: formatPath(rp),
            message: `expected ${schema.kind}, got ${tag}`,
          }),
        );
      }
      return;
    }
    case "list": {
      if (!Array.isArray(value)) {
        out.push(
          makeIssue({
            kind: "type",
            instancePath: formatPath(ip),
            rulePath: formatPath(rp),
            message: `expected list, got ${typeOf(value)}`,
          }),
        );
        return;
      }
      value.forEach((item, index) => checkNode(item, schema.items, [...ip, index], [...rp, "items"], out));
      return;
    }
    case "object": {
      if (typeOf(value) !== "object") {
        out.push(
          makeIssue({
            kind: "type",
            instancePath: formatPath(ip),
            rulePath: formatPath(rp),
            message: `expected object, got ${typeOf(value)}`,
          }),
        );
        return;
      }
      const obj = value as Record<string, unknown>;
      for (const name of schema.required) {
        if (!Object.prototype.hasOwnProperty.call(obj, name)) {
          out.push(
            makeIssue({
              kind: "missing-required",
              instancePath: formatPath([...ip, name]),
              rulePath: formatPath([...rp, "required"]),
              message: `required field "${name}" is missing`,
              related: [{ rulePath: formatPath([...rp, "fields", name]), note: "declared here" }],
            }),
          );
        }
      }
      for (const [name, sub] of Object.entries(schema.fields)) {
        if (Object.prototype.hasOwnProperty.call(obj, name)) {
          checkNode(obj[name], sub, [...ip, name], [...rp, "fields", name], out);
        }
      }
      return;
    }
  }
}

/** Resolve a schema node at an instance path; null when the path is not described. */
export function schemaAt(schema: Schema, segments: readonly string[]): Schema | null {
  let current: Schema = schema;
  for (const segment of segments) {
    if (current.kind === "object") {
      const next = current.fields[segment];
      if (next === undefined) return null;
      current = next;
    } else if (current.kind === "list") {
      if (!/^(0|[1-9]\d*)$/.test(segment)) return null;
      current = current.items;
    } else {
      return null;
    }
  }
  return current;
}

export function coversPath(schema: Schema, segments: readonly string[]): boolean {
  return schemaAt(schema, segments) !== null;
}
