/** Machine-readable validation error codes. */
export type ErrorCode =
  | "UnknownSchemaVersion"
  | "UnknownType"
  | "MissingRequiredField"
  | "FieldTypeMismatch"
  | "MissingTarget"
  | "TargetTypeMismatch"
  | "ReverseCardinality"
  | "DuplicateId"
  | "RenameConflict"
  | "MissingDocument"
  | "DeleteReferenced";

/**
 * A single validation failure.
 *
 * Every error carries:
 *  - instancePath: JSON-pointer-ish path into the offending document ("original path")
 *  - rulePath:     path into the (versioned) schema that produced the rule
 *  - slice:        the minimal relevant document slice (the offending subvalue,
 *                  or the small set of referring locations for reverse rules)
 */
export interface ValidationError {
  code: ErrorCode;
  docId?: string;
  instancePath: string;
  rulePath: string;
  message: string;
  slice: unknown;
}

export function formatError(err: ValidationError): string {
  const where = err.docId ? `doc=${err.docId} ` : "";
  return `[${err.code}] ${where}at ${err.instancePath} (rule ${err.rulePath}): ${err.message}`;
}
