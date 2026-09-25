/**
 * Explicit attribute schema.
 *
 * Security boundary: policies may read ONLY attributes declared here, and only
 * with the declared finite-set / integer / boolean operations. No external
 * identity service or arbitrary property lookup is ever consulted; the
 * evaluator rejects unknown attributes up front.
 */

export interface FiniteAttr {
  kind: "finite";
  values: readonly string[]; // nonempty finite universe
}

export interface IntAttr {
  kind: "int";
  min: number;
  max: number; // inclusive, finite bounds -> the integer universe stays finite
}

export interface BoolAttr {
  kind: "bool";
}

export type AttrSchema = FiniteAttr | IntAttr | BoolAttr;

export type Schema = Readonly<Record<string, AttrSchema>>;

/** One concrete input: absent attributes simply do not appear. */
export type Assignment = Readonly<Record<string, string | number | boolean>>;

export class SchemaError extends Error {}

export function validateSchema(schema: Schema): void {
  for (const [name, attr] of Object.entries(schema)) {
    if (attr.kind === "finite") {
      if (attr.values.length === 0) {
        throw new SchemaError(`finite attribute ${name} must have at least one value`);
      }
      if (new Set(attr.values).size !== attr.values.length) {
        throw new SchemaError(`finite attribute ${name} has duplicate values`);
      }
    } else if (attr.kind === "int") {
      if (!Number.isInteger(attr.min) || !Number.isInteger(attr.max) || attr.min > attr.max) {
        throw new SchemaError(`int attribute ${name} needs integer min <= max`);
      }
    }
  }
}

export function collectNames(node: unknown, acc: Set<string> = new Set()): Set<string> {
  if (!node || typeof node !== "object") return acc;
  const n = node as { kind?: string; name?: string; arg?: unknown; args?: unknown[] };
  if (typeof n.name === "string") acc.add(n.name);
  if (n.arg) collectNames(n.arg, acc);
  if (Array.isArray(n.args)) for (const a of n.args) collectNames(a, acc);
  return acc;
}

/** Ensure every referenced attribute is explicitly declared; no implicit reads. */
export function assertDeclared(expr: unknown, schema: Schema): void {
  for (const name of collectNames(expr)) {
    if (!(name in schema)) {
      throw new SchemaError(
        `attribute "${name}" is not declared in the schema (implicit/external reads are forbidden)`,
      );
    }
  }
}

export function validateAssignment(a: Assignment, schema: Schema): void {
  for (const [name, v] of Object.entries(a)) {
    const attr = schema[name];
    if (!attr) throw new SchemaError(`assignment uses undeclared attribute "${name}"`);
    if (attr.kind === "bool" && typeof v !== "boolean") {
      throw new SchemaError(`${name} must be boolean`);
    }
    if (attr.kind === "finite" && (typeof v !== "string" || !attr.values.includes(v))) {
      throw new SchemaError(`${name} must be one of {${attr.values.join(",")}}`);
    }
    if (attr.kind === "int" && (typeof v !== "number" || !Number.isInteger(v) || v < attr.min || v > attr.max)) {
      throw new SchemaError(`${name} must be an integer in [${attr.min}, ${attr.max}]`);
    }
  }
}

/** Enumerate the full finite input domain, including the absent ("missing") case. */
export function enumerateAssignments(schema: Schema): Assignment[] {
  const entries = Object.entries(schema);
  let out: Assignment[] = [{}];
  for (const [name, attr] of entries) {
    const values: Array<string | number | boolean | undefined> =
      attr.kind === "bool"
        ? [undefined, true, false]
        : attr.kind === "finite"
          ? [undefined, ...attr.values]
          : [undefined, ...rangeInclusive(attr.min, attr.max)] as Array<number | undefined>;
    const next: Assignment[] = [];
    for (const base of out) {
      for (const v of values) {
        next.push(v === undefined ? { ...base } : { ...base, [name]: v });
      }
    }
    out = next;
  }
  return out;
}

function rangeInclusive(min: number, max: number): number[] {
  const out: number[] = [];
  for (let v = min; v <= max; v++) out.push(v);
  return out;
}

export function domainSize(schema: Schema): bigint {
  let n = 1n;
  for (const attr of Object.values(schema)) {
    n *=
      attr.kind === "bool" ? 3n : attr.kind === "finite" ? BigInt(attr.values.length + 1) : BigInt(attr.max - attr.min + 2);
  }
  return n;
}
