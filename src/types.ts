// Implemented schema subset (this is ALL we promise to emit or validate):
//   type: "object" | "array" | "string" | "number" | "integer" | "boolean" | "null"
//         (or an array of those, for unions / nullable)
//   object:  properties, required        (additionalProperties is NEVER emitted as false)
//   array:   items, minItems, maxItems
//   string:  minLength, maxLength, enum
//   number/integer: minimum, maximum
// Anything outside this subset is reported as "unknown", never guessed.

export type PrimitiveType =
  | "object"
  | "array"
  | "string"
  | "number"
  | "integer"
  | "boolean"
  | "null";

export type TypeSpec = PrimitiveType | PrimitiveType[];

export interface Schema {
  type?: TypeSpec;
  properties?: Record<string, Schema>;
  required?: string[];
  items?: Schema;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  enum?: unknown[];
}

export interface ValidationError {
  instancePath: string; // e.g. $.user.age
  rulePath: string;     // e.g. #/properties/user/properties/age/type
  message: string;
}

export interface Uncertainty {
  path: string;
  axis: string;
  observation: string;
  caveat: string;
}

export interface Axis {
  kind:
    | "integer-vs-number"
    | "numeric-range"
    | "required"
    | "string-enum"
    | "string-length"
    | "array-length";
  path: string;      // instance path of the field (parent path for "required")
  key?: string;      // set for "required" axes
  rulePath: string;  // where the differing rule lives in the strict schema
  detail: string;
}

export interface Candidate {
  rank: number;
  profile: "generalized" | "observed-fit";
  schema: Schema;
  support: { accepted: number; total: number };
  inductiveAssumptions: number;
  selfCheck: {
    allRetainedSamplesAccepted: boolean;
    failures: { sampleId: string; errors: ValidationError[] }[];
  };
}

export interface Distinguisher {
  axis: Axis["kind"];
  path: string;
  description: string;
  instance: unknown;
  acceptedBy: string[];
  rejectedBy: { profile: string; error: ValidationError }[];
  verified: boolean;
}
