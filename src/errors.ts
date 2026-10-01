/** Minimal slice of a document relevant to an error. */
export interface DocSlice {
  /** Document id the slice comes from. */
  id: string;
  /** Original JSON-pointer path into the document's data ("" = whole data). */
  path: string;
  /** Value found at that path. */
  value: unknown;
}

/**
 * A single validation / integrity error.
 * Always carries both the instance path (where in the data) and the
 * rule path (where in the schema rule tree) when a schema rule is involved.
 */
export interface ValidationError {
  code:
    | "unsupported-schema"   // schema uses keywords outside the promised subset
    | "unknown-schema"       // document references an unregistered type/version
    | "duplicate-id"         // same id in two different documents
    | "type-mismatch"        // value does not match the declared kind
    | "missing-required"     // required property absent
    | "unknown-property"     // property not declared in the object rule
    | "enum-mismatch"        // value not in enum list
    | "array-size"           // minItems/maxItems violated
    | "ref-missing"          // referenced id does not exist
    | "ref-type"             // referenced document has a disallowed type
    | "ref-not-string"       // reference value is not a string id
    | "ref-max-incoming"     // reverse cardinality exceeded
    | "delete-referenced"    // delete of a still-referenced document
    | "rename-missing"       // rename of a non-existent id
    | "delete-missing";      // delete of a non-existent id
  message: string;
  /** Original JSON-pointer path into the offending document's data. */
  instancePath: string;
  /** Path into the schema rule tree ("" when not schema-rule related). */
  rulePath: string;
  /** Minimal relevant document slices (only the involved docs/paths). */
  docs: DocSlice[];
}

export function makeError(
  code: ValidationError["code"],
  message: string,
  instancePath: string,
  rulePath: string,
  docs: DocSlice[],
): ValidationError {
  return { code, message, instancePath, rulePath, docs };
}
