export type Json =
  | string
  | number
  | boolean
  | null
  | Json[]
  | { [key: string]: Json };

export type PrimitiveTypeName = "string" | "number" | "integer" | "boolean";
export type TypeName = PrimitiveTypeName | "object" | "array";

export interface AggregateRule {
  key: string;
  sum?: { minimum?: number; maximum?: number };
}

export interface Schema {
  type?: TypeName;
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  uniqueKeys?: string[];
  aggregates?: AggregateRule[];
  properties?: Record<string, Schema>;
  required?: string[];
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  enum?: Json[];
  pattern?: string;
}

export type OnError = "collect" | "throw";

export interface ValidationIssue {
  instancePath: string;
  rulePath: string;
  rule: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  state: "ended";
  length: number;
  issues: ValidationIssue[];
  sums: Record<string, number>;
}

export interface StreamStatus {
  state: "open" | "ended";
  length: number;
  valid: boolean;
}
