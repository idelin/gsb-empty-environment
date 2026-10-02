import type { FieldDescriptor, MessageDescriptor } from './schema.ts';

export interface ValidationIssue {
  path: string;
  field: string;
  rule: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  issues: ValidationIssue[];
}

function checkField(
  field: FieldDescriptor,
  value: unknown,
  path: string,
  issues: ValidationIssue[],
  depth: number,
  maxDepth: number,
): void {
  const fieldPath = path === '' ? field.name : `${path}.${field.name}`;
  if (depth > maxDepth) {
    issues.push({ path: fieldPath, field: field.name, rule: 'depth', message: `exceeds max depth ${maxDepth}` });
    return;
  }
  const values = field.repeated ? (value as unknown[]) : [value];
  for (const v of values) {
    if (field.validate) {
      const problem = field.validate(v);
      if (problem !== undefined) {
        issues.push({ path: fieldPath, field: field.name, rule: 'custom', message: problem });
      }
    }
    if (field.type === 'message' && field.message && v !== null && typeof v === 'object') {
      validateObject(v as Record<string, unknown>, field.message, fieldPath, issues, depth + 1, maxDepth);
    }
  }
}

function validateObject(
  obj: Record<string, unknown>,
  descriptor: MessageDescriptor,
  path: string,
  issues: ValidationIssue[],
  depth: number,
  maxDepth: number,
): void {
  for (const field of descriptor.fields) {
    const value = obj[field.name];
    const missing = value === undefined || (field.repeated && (value as unknown[]).length === 0);
    if (field.required && missing) {
      const fieldPath = path === '' ? field.name : `${path}.${field.name}`;
      issues.push({ path: fieldPath, field: field.name, rule: 'required', message: 'required field missing' });
      continue;
    }
    if (!missing) {
      checkField(field, value, path, issues, depth, maxDepth);
    }
  }
}

/** Reflection-mode validation of a decoded object against its descriptor. */
export function validate(
  obj: Record<string, unknown>,
  descriptor: MessageDescriptor,
  maxDepth = 100,
): ValidationResult {
  const issues: ValidationIssue[] = [];
  validateObject(obj, descriptor, '', issues, 0, maxDepth);
  return { valid: issues.length === 0, issues };
}
