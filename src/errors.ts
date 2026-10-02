export type ErrorCode =
  | "UNKNOWN_TYPE"
  | "MISSING_REQUIRED"
  | "TYPE_MISMATCH"
  | "REF_NOT_FOUND"
  | "REF_WRONG_TYPE"
  | "MAX_INCOMING_EXCEEDED"
  | "DUPLICATE_ID"
  | "DELETE_REFERENCED"
  | "RENAME_CONFLICT"
  | "NOT_FOUND";

/**
 * Every validation error carries:
 * - docId:       the document where the problem manifests
 * - instancePath: JSON pointer into that document ("" = document root)
 * - schemaPath:  JSON-pointer-like path of the violated rule
 *                (e.g. "#/types/book/properties/author")
 * - slice:       the minimal relevant slice of the document at instancePath
 */
export interface ValidationError {
  code: ErrorCode;
  docId: string;
  instancePath: string;
  schemaPath: string;
  slice: unknown;
  message: string;
}
