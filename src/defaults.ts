import type { JsonValue, Schema, ValidationError } from "./schema.ts";
import { getAt, isPlainObject, parsePointer, setAt, toPointer } from "./pointer.ts";

class CycleError extends Error {
  readonly chain: string[];

  constructor(chain: string[]) {
    super(`circular default dependency: ${chain.join(" -> ")}`);
    this.chain = chain;
  }
}

type Resolution = { ok: true; value: JsonValue } | { ok: false; reason: string };

interface LocatedSchema {
  schema: Schema;
  schemaPath: string;
}

function extractFrom(expr: JsonValue): string | undefined {
  if (isPlainObject(expr) && typeof expr.$from === "string" && Object.keys(expr).length === 1) {
    return expr.$from;
  }
  return undefined;
}

/** "./x" resolves against the site's parent object; anything else is an absolute pointer. */
function resolveRef(ref: string, siteSegments: string[]): string[] {
  if (ref.startsWith("./")) {
    return [...siteSegments.slice(0, -1), ...parsePointer(ref.slice(1))];
  }
  return parsePointer(ref);
}

function locateSchema(root: Schema, segments: string[]): LocatedSchema | undefined {
  let schema: Schema = root;
  let schemaPath = "#";
  for (const segment of segments) {
    if (schema.properties !== undefined && segment in schema.properties) {
      schema = schema.properties[segment];
      schemaPath += `/properties/${segment}`;
    } else if (schema.items !== undefined && /^\d+$/.test(segment)) {
      schema = schema.items;
      schemaPath += "/items";
    } else {
      return undefined;
    }
  }
  return { schema, schemaPath };
}

/**
 * Fills defaults for missing fields on a (already cloned) document.
 *
 * "Missing" means the key is absent; an explicit null is a present value
 * and never triggers a default. Defaults that reference other fields via
 * `{ $from }` are resolved on demand in dependency (topological) order,
 * regardless of where the referenced field lives in the document.
 */
export class DefaultResolver {
  readonly errors: ValidationError[] = [];
  private readonly state = new Map<string, "visiting" | "done">();
  private readonly failed = new Set<string>();
  private insertions = 0;
  private readonly rootSchema: Schema;
  private readonly root: JsonValue;

  constructor(rootSchema: Schema, root: JsonValue) {
    this.rootSchema = rootSchema;
    this.root = root;
  }

  fillAll(): void {
    // Inserted subtrees may contain their own defaultable fields, so
    // iterate until a full walk makes no progress.
    for (;;) {
      const before = this.insertions;
      this.walk(this.rootSchema, [], this.root);
      if (this.insertions === before) return;
    }
  }

  private walk(schema: Schema, segments: string[], node: JsonValue): void {
    if (isPlainObject(node) && schema.properties !== undefined) {
      for (const [key, subschema] of Object.entries(schema.properties)) {
        const childSegments = [...segments, key];
        if (key in node) {
          this.walk(subschema, childSegments, node[key]);
        } else if ("default" in subschema && !this.failed.has(toPointer(childSegments))) {
          this.tryResolve(childSegments);
          if (key in node) this.walk(subschema, childSegments, node[key]);
        }
      }
    } else if (Array.isArray(node) && schema.items !== undefined) {
      node.forEach((item, index) => {
        this.walk(schema.items!, [...segments, String(index)], item);
      });
    }
  }

  private tryResolve(segments: string[]): void {
    const pointer = toPointer(segments);
    const schemaPath = (locateSchema(this.rootSchema, segments)?.schemaPath ?? "#") + "/default";
    try {
      const result = this.resolve(segments, []);
      if (!result.ok) {
        this.failed.add(pointer);
        this.errors.push({ instancePath: pointer, schemaPath, message: result.reason });
      }
    } catch (error) {
      if (error instanceof CycleError) {
        for (const link of error.chain) this.failed.add(link);
        this.errors.push({ instancePath: pointer, schemaPath, message: error.message });
      } else {
        throw error;
      }
    }
  }

  private resolve(segments: string[], stack: string[]): Resolution {
    const existing = getAt(this.root, segments);
    if (existing.found) return { ok: true, value: existing.value! };

    const pointer = toPointer(segments);
    if (this.failed.has(pointer)) {
      return { ok: false, reason: `dependency '${pointer}' could not be resolved` };
    }
    if (this.state.get(pointer) === "visiting") {
      throw new CycleError([...stack.slice(stack.indexOf(pointer)), pointer]);
    }

    const located = locateSchema(this.rootSchema, segments);
    if (located === undefined || !("default" in located.schema)) {
      return { ok: false, reason: `'${pointer}' is missing and has no default` };
    }

    if (segments.length > 0) {
      const parent = this.resolve(segments.slice(0, -1), stack);
      if (!parent.ok) {
        this.failed.add(pointer);
        return { ok: false, reason: `parent of '${pointer}' is unavailable` };
      }
    }

    this.state.set(pointer, "visiting");
    stack.push(pointer);
    try {
      const expr = located.schema.default as JsonValue;
      let from: string | undefined;
      try {
        from = extractFrom(expr);
      } catch {
        from = undefined;
      }
      let value: JsonValue;
      if (from !== undefined) {
        let targetSegments: string[];
        try {
          targetSegments = resolveRef(from, segments);
        } catch {
          this.failed.add(pointer);
          return { ok: false, reason: `invalid $from pointer ${JSON.stringify(from)}` };
        }
        const dependency = this.resolve(targetSegments, stack);
        if (!dependency.ok) {
          this.failed.add(pointer);
          return { ok: false, reason: `default for '${pointer}' depends on ${dependency.reason}` };
        }
        value = structuredClone(dependency.value);
      } else {
        // Deep clone so every application gets its own mutable object —
        // array elements never share the schema's default instance.
        value = structuredClone(expr);
      }
      setAt(this.root, segments, value);
      this.insertions += 1;
      this.state.set(pointer, "done");
      return { ok: true, value };
    } finally {
      stack.pop();
    }
  }
}
