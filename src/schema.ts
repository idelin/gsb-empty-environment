/**
 * Versioned schema definitions.
 *
 * Only this subset is promised/implemented (see README):
 *  - object types with named fields
 *  - field kinds: "string" | "number" | "boolean" | "ref" | "ref[]"
 *  - optional/required fields
 *  - ref target types (checked against the *referencing* document's schema version)
 *  - reverse cardinality per target type: incoming { min, max }
 */

export type PrimitiveKind = "string" | "number" | "boolean";

export type FieldDef =
  | { kind: PrimitiveKind; required?: boolean }
  | { kind: "ref"; target: string; required?: boolean }
  | { kind: "ref[]"; target: string; required?: boolean };

export interface TypeDef {
  fields: Record<string, FieldDef>;
  /** Reverse cardinality: how many incoming references a document of this type must carry. */
  incoming?: { min?: number; max?: number };
}

export interface SchemaVersion {
  version: string;
  types: Record<string, TypeDef>;
}

export class SchemaRegistry {
  private readonly versions = new Map<string, SchemaVersion>();

  register(schema: SchemaVersion): this {
    if (this.versions.has(schema.version)) {
      throw new Error(`schema version already registered: ${schema.version}`);
    }
    this.versions.set(schema.version, schema);
    return this;
  }

  get(version: string): SchemaVersion | undefined {
    return this.versions.get(version);
  }
}
