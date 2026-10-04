/** Post-merge validation: null values and schema type checks, all located. */
import type { ConfigSchema, FieldType, MergeIssue } from './types.ts';
import type { MergeResult } from './merge.ts';
import type { ProvNode } from './provenance.ts';

function typeOf(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  return typeof value;
}

function matchesType(value: unknown, type: FieldType): boolean {
  switch (type) {
    case 'any':
      return true;
    case 'string':
      return typeof value === 'string';
    case 'number':
      return typeof value === 'number' && !Number.isNaN(value);
    case 'boolean':
      return typeof value === 'boolean';
    case 'string[]':
      return Array.isArray(value) && value.every((v) => typeof v === 'string');
    case 'number[]':
      return Array.isArray(value) && value.every((v) => typeof v === 'number');
    case 'object':
      return typeof value === 'object' && value !== null && !Array.isArray(value);
  }
}

function walkNulls(
  value: unknown,
  node: ProvNode,
  path: string,
  schema: ConfigSchema,
  issues: MergeIssue[],
): void {
  if (value === null) {
    if (path !== '' && schema[path]?.nullable === true) return;
    const source = node.entries[0]?.source;
    issues.push({
      severity: 'error',
      code: 'null-value',
      path,
      message: `null value at '${path === '' ? '<root>' : path}'`,
      ...(source !== undefined ? { source } : {}),
    });
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, i) => {
      const child = node.children.get(String(i));
      if (child !== undefined) {
        walkNulls(item, child, `${path}[${i}]`, schema, issues);
      }
    });
    return;
  }
  if (typeof value === 'object') {
    for (const [k, v] of Object.entries(value)) {
      const child = node.children.get(k);
      if (child !== undefined) {
        walkNulls(v, child, path === '' ? k : `${path}.${k}`, schema, issues);
      }
    }
  }
}

function lookupPath(value: unknown, path: string): unknown {
  let cur = value;
  for (const seg of path.replace(/\[(\d+)\]/g, '.$1').split('.')) {
    if (typeof cur !== 'object' || cur === null) return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

export function validate(result: MergeResult, schema: ConfigSchema = {}): MergeIssue[] {
  const issues: MergeIssue[] = [];
  walkNulls(result.value, result.provenance, '', schema, issues);

  for (const [path, field] of Object.entries(schema)) {
    const value = lookupPath(result.value, path);
    if (value === undefined || value === null) continue;
    if (!matchesType(value, field.type)) {
      issues.push({
        severity: 'error',
        code: 'type-mismatch',
        path,
        message: `expected ${field.type} at '${path}', got ${typeOf(value)}`,
      });
    }
  }
  return issues;
}
