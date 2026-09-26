// Minimal distinguishing instances between candidates. For every axis where
// the two profiles disagree, build ONE small JSON value that the generalized
// candidate accepts and the observed-fit candidate rejects. Each generated
// instance is verified with our own validator before it is reported.

import type { Axis, Distinguisher, Schema } from "./types.ts";
import { validate } from "./validate.ts";

type Seg = { kind: "key"; key: string } | { kind: "items" };

export function parsePath(path: string): Seg[] {
  const segs: Seg[] = [];
  let i = 1; // skip leading $
  while (i < path.length) {
    if (path[i] === ".") {
      const m = /^[A-Za-z_$][A-Za-z0-9_$]*/.exec(path.slice(i + 1));
      if (!m) throw new Error(`cannot parse path: ${path}`);
      segs.push({ kind: "key", key: m[0] });
      i += 1 + m[0].length;
    } else if (path[i] === "[") {
      if (path[i + 1] === "]") {
        segs.push({ kind: "items" });
        i += 2;
      } else {
        const end = path.indexOf("]", i);
        segs.push({ kind: "key", key: JSON.parse(path.slice(i + 1, end)) as string });
        i = end + 1;
      }
    } else {
      throw new Error(`cannot parse path: ${path}`);
    }
  }
  return segs;
}

// Smallest value accepted by the (strict) schema; used as the base instance
// that each distinguisher then mutates along exactly one axis.
export function minimalInstance(schema: Schema): unknown {
  if (schema.enum !== undefined && schema.enum.length > 0) return schema.enum[0];
  let t = schema.type;
  if (Array.isArray(t)) t = t.find((x) => x !== "null") ?? "null";
  switch (t) {
    case "object": {
      const obj: Record<string, unknown> = {};
      for (const key of schema.required ?? []) {
        obj[key] = minimalInstance(schema.properties?.[key] ?? {});
      }
      return obj;
    }
    case "array":
      return Array.from({ length: schema.minItems ?? 0 }, () =>
        minimalInstance(schema.items ?? {}),
      );
    case "string":
      return "x".repeat(schema.minLength ?? 0);
    case "integer":
    case "number":
      return schema.minimum ?? 0;
    case "boolean":
      return false;
    case "null":
      return null;
    default:
      if (schema.properties) return minimalInstance({ ...schema, type: "object" });
      if (schema.items) return minimalInstance({ ...schema, type: "array" });
      return null;
  }
}

function schemaAt(schema: Schema, segs: Seg[]): Schema {
  let cur = schema;
  for (const seg of segs) {
    cur = seg.kind === "key" ? (cur.properties?.[seg.key] ?? {}) : (cur.items ?? {});
  }
  return cur;
}

// Walk root along segs[:-1], creating minimal containers where the base
// instance lacks them, and return the parent container of the last segment.
function ensureParent(
  root: unknown,
  schema: Schema,
  segs: Seg[],
): { parent: Record<string, unknown> | unknown[]; leafSchema: Schema } {
  let cur = root as Record<string, unknown> & unknown[];
  let curSchema = schema;
  for (let i = 0; i < segs.length - 1; i++) {
    const seg = segs[i];
    if (seg.kind === "key") {
      const nextSchema = curSchema.properties?.[seg.key] ?? {};
      if (cur[seg.key] === undefined || cur[seg.key] === null) {
        cur[seg.key] = minimalInstance(nextSchema);
      }
      cur = cur[seg.key] as Record<string, unknown> & unknown[];
      curSchema = nextSchema;
    } else {
      const nextSchema = curSchema.items ?? {};
      if (!Array.isArray(cur)) throw new Error("expected array container");
      if (cur.length === 0) cur.push(minimalInstance(nextSchema));
      cur = cur[0] as Record<string, unknown> & unknown[];
      curSchema = nextSchema;
    }
  }
  return { parent: cur, leafSchema: schemaAt(schema, segs) };
}

function setLeaf(parent: Record<string, unknown> & unknown[], seg: Seg, value: unknown): void {
  if (seg.kind === "key") parent[seg.key] = value;
  else {
    if (!Array.isArray(parent)) throw new Error("expected array container");
    if (parent.length === 0) parent.push(value);
    else parent[0] = value;
  }
}

function unseenString(enumValues: unknown[]): string {
  let candidate = "__unseen__";
  while (enumValues.includes(candidate)) candidate += "_";
  return candidate;
}

function buildForAxis(axis: Axis, strict: Schema): unknown {
  const instance = minimalInstance(strict) as Record<string, unknown> & unknown[];
  if (axis.kind === "required") {
    const segs = [...parsePath(axis.path), { kind: "key", key: axis.key as string } as Seg];
    const { parent } = ensureParent(instance, strict, segs);
    delete (parent as Record<string, unknown>)[axis.key as string];
    return instance;
  }
  const segs = parsePath(axis.path);
  const { parent, leafSchema } = ensureParent(instance, strict, segs);
  const last = segs[segs.length - 1];
  switch (axis.kind) {
    case "integer-vs-number":
      setLeaf(parent, last, (leafSchema.minimum ?? 0) + 0.5);
      break;
    case "numeric-range":
      setLeaf(
        parent,
        last,
        leafSchema.maximum !== undefined
          ? leafSchema.maximum + 1
          : (leafSchema.minimum ?? 0) - 1,
      );
      break;
    case "string-enum":
      setLeaf(parent, last, unseenString(leafSchema.enum ?? []));
      break;
    case "string-length":
      setLeaf(parent, last, "x".repeat((leafSchema.maxLength ?? 0) + 1));
      break;
    case "array-length": {
      const items = leafSchema.items ?? {};
      setLeaf(
        parent,
        last,
        Array.from({ length: (leafSchema.maxItems ?? 0) + 1 }, () => minimalInstance(items)),
      );
      break;
    }
  }
  return instance;
}

export function buildDistinguishers(
  axes: Axis[],
  strict: Schema,
  generalized: Schema,
): Distinguisher[] {
  const out: Distinguisher[] = [];
  for (const axis of axes) {
    let instance: unknown;
    try {
      instance = buildForAxis(axis, strict);
    } catch {
      continue; // cannot construct this axis's instance; skip rather than guess
    }
    const genErrors = validate(instance, generalized);
    const strictErrors = validate(instance, strict);
    const verified = genErrors.length === 0 && strictErrors.length > 0;
    out.push({
      axis: axis.kind,
      path: axis.kind === "required" ? `${axis.path}.${axis.key}` : axis.path,
      description: axis.detail,
      instance,
      acceptedBy: genErrors.length === 0 ? ["generalized"] : [],
      rejectedBy:
        strictErrors.length > 0 ? [{ profile: "observed-fit", error: strictErrors[0] }] : [],
      verified,
    });
  }
  return out;
}
