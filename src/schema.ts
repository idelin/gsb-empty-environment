/** A finite JSON instance value. */
export type JSONValue =
  | null
  | boolean
  | number
  | string
  | JSONValue[]
  | { [key: string]: JSONValue };

export type PrimitiveTypeName = "null" | "boolean" | "number" | "string";

export interface ObjectSchema {
  type: "object";
  properties?: { [key: string]: Schema };
  required?: string[];
}

export interface ArraySchema {
  type: "array";
  items?: Schema;
  minItems?: number;
  maxItems?: number;
}

/** Mutually exclusive branches: exactly one branch must match. */
export interface OneOfSchema {
  oneOf: Schema[];
}

/**
 * A reference to another schema node, allowing recursion.
 * Supported pointers: "#" (the root document) and "#/definitions/<name>".
 */
export interface RefSchema {
  $ref: string;
}

export type Schema =
  | { type: PrimitiveTypeName }
  | ObjectSchema
  | ArraySchema
  | OneOfSchema
  | RefSchema;

/** The root schema may carry shared definitions referenced by $ref. */
export type SchemaDocument = Schema & {
  definitions?: { [name: string]: Schema };
};

export type PathSegment = string | number;

export interface BranchFailure {
  /** Index of the oneOf branch that failed. */
  branch: number;
  /** That branch's own shallowest error. */
  error: ValidationError;
}

export interface ValidationError {
  /** Instance path of the failure, e.g. ["items", 2, "name"]. */
  path: PathSegment[];
  message: string;
  /** Per-branch explanations when a oneOf node fails. */
  branches?: BranchFailure[];
}

export type ValidationResult =
  | { valid: true }
  | { valid: false; error: ValidationError };

export interface ValidationOptions {
  /**
   * Budget on container nesting depth. The root sits at depth 0 and each
   * nested object/array adds one; scalar leaves do not consume budget.
   * A container deeper than maxDepth is a validation failure.
   * Defaults to Infinity.
   */
  maxDepth?: number;
}
