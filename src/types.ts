export type ScimMutability =
  | "readOnly"
  | "readWrite"
  | "immutable"
  | "writeOnly";

export interface ScimAttributeSchema {
  name: string;
  type?: string;
  multiValued?: boolean;
  mutability?: ScimMutability | string;
  subAttributes?: readonly ScimAttributeSchema[];
  [key: string]: unknown;
}

export interface ScimSchema {
  id?: string;
  schema?: string;
  schemaId?: string;
  urn?: string;
  schemaUrn?: string;
  name?: string;
  attributes?: readonly ScimAttributeSchema[];
  [key: string]: unknown;
}

export type ScimSchemaMapValue =
  | readonly ScimAttributeSchema[]
  | ScimSchema
  | ScimAttributeSchema
  | string
  | { attributes?: readonly ScimAttributeSchema[] };

export type ScimSchemaInput =
  | readonly ScimSchema[]
  | readonly ScimAttributeSchema[]
  | ScimSchema
  | ScimAttributeSchema
  | Readonly<Record<string, ScimSchemaMapValue>>
  | { schemas?: readonly ScimSchema[] };

export interface ScimPatchOperation {
  op: string;
  path?: string;
  value?: unknown;
  [key: string]: unknown;
}

export interface ScimPatchRequest {
  Operations?: readonly ScimPatchOperation[];
  operations?: readonly ScimPatchOperation[];
  schemas?: readonly string[];
  [key: string]: unknown;
}

export interface ParsedFilter {
  raw: string;
  attribute: string;
  attributeSegments: readonly string[];
  op: string;
  value: unknown;
  valueRaw: string;
}

export interface ParsedScimPath {
  raw: string;
  schemaUrn?: string;
  attributeSegments: readonly string[];
  subAttributeSegments: readonly string[];
  targetSegments: readonly string[];
  filter?: ParsedFilter;
}

export type PatchViolationReason =
  | "invalidPatch"
  | "invalidOperation"
  | "invalidPath"
  | "invalidFilter"
  | "invalidValue"
  | "unknownAttribute"
  | "ambiguousAttribute"
  | "readOnly"
  | "immutable"
  | "notWritable";

export interface PatchViolation {
  operationIndex: number;
  op: string | null;
  path: string | null;
  attributePath: string;
  reason: PatchViolationReason;
  message: string;
  mutability?: ScimMutability;
  status: number;
}

export interface PatchValidationResult {
  valid: boolean;
  ok: boolean;
  allowed: boolean;
  violations: readonly PatchViolation[];
  errors: readonly PatchViolation[];
}

export type WritableAttributeInput =
  | string
  | readonly string[]
  | ReadonlySet<string>
  | ReadonlyMap<string, boolean>
  | Readonly<Record<string, boolean>>;

export interface PatchValidationOptions {
  schemas?: ScimSchemaInput;
  schema?: ScimSchemaInput;

  /**
   * Attributes (or path prefixes) the caller is allowed to patch. An entry
   * such as "name" permits "name.givenName"; an entry such as
   * "emails[type eq \"work\"].value" only permits that filtered leaf.
   * Omit this option to rely on schema mutability alone. An explicitly empty
   * collection denies every attribute.
   */
  writableAttributes?: WritableAttributeInput;
  writablePaths?: WritableAttributeInput;
  allowedAttributes?: WritableAttributeInput;
  allowedPaths?: WritableAttributeInput;
  writable?: WritableAttributeInput;
  writableAttributeSet?: WritableAttributeInput;
  callerWritableAttributes?: WritableAttributeInput;

  /**
   * Optional caller overrides. They can be used when the caller has a
   * tenant-specific policy on top of the discovery schema.
   */
  attributeMutability?: Readonly<Record<string, ScimMutability | string>>;
  mutability?: Readonly<Record<string, ScimMutability | string>>;
  schemaMutability?: Readonly<Record<string, ScimMutability | string>>;
  readOnlyAttributes?: readonly string[];
  immutableAttributes?: readonly string[];
}

export interface AttributeEntry {
  schemaId: string | null;
  schemaDisplayId: string | null;
  schemaIsCore: boolean;
  segments: readonly string[];
  attribute: ScimAttributeSchema;
  effectiveMutability: ScimMutability;
  fullKey: string;
  bareKey: string;
}

export interface SchemaIndex {
  entries: readonly AttributeEntry[];
  byFull: ReadonlyMap<string, AttributeEntry>;
  byBare: ReadonlyMap<string, readonly AttributeEntry[]>;
  knownSchemaUrns: readonly string[];
}

export interface ResolvedTarget {
  entry: AttributeEntry;
  filter?: ParsedFilter;
  attributePath: string;
}

export interface WritableGrant {
  schemaId: string | null;
  segments: readonly string[];
  filter?: ParsedFilter;
  allSchema: boolean;
}
