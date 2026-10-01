export type Segment = string | number;

export interface ValidationError {
  schemaVersion: string;
  keyword: string;
  instancePath: Segment[];
  schemaPath: string;
  message: string;
}

export interface Schema {
  type?: string | string[];
  enum?: unknown[];
  const?: unknown;
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  minimum?: number;
  maximum?: number;
  multipleOf?: number;
}
