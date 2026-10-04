export type Json =
  | null
  | boolean
  | number
  | string
  | Json[]
  | { [key: string]: Json };

export interface BaseSchema {
  $defs?: Record<string, Schema>;
}

export interface PrimitiveSchema extends BaseSchema {
  type: 'null' | 'boolean' | 'number' | 'integer' | 'string';
}

export interface ArraySchema extends BaseSchema {
  type: 'array';
  items?: Schema;
  minItems?: number;
  maxItems?: number;
}

export interface ObjectSchema extends BaseSchema {
  type: 'object';
  properties?: Record<string, Schema>;
  required?: string[];
}

export interface RefSchema extends BaseSchema {
  $ref: string;
}

export interface OneOfSchema extends BaseSchema {
  oneOf: Schema[];
}

export type Schema =
  | PrimitiveSchema
  | ArraySchema
  | ObjectSchema
  | RefSchema
  | OneOfSchema;

export type PathSegment = string | number;

export interface BranchFailure {
  branch: number;
  error: ValidationError;
}

export interface ValidationError {
  path: PathSegment[];
  message: string;
  branches?: BranchFailure[];
}

export interface ValidateOptions {
  maxDepth?: number;
}

export type ValidationResult =
  | { valid: true }
  | { valid: false; error: ValidationError };
