/**
 * Schema describing a JSON-compatible tree. Object keys are unordered,
 * arrays are ordered. Numeric leaves carry a unit and may declare an
 * integer target precision, which requires an explicit rounding mode.
 */

import type { RoundingMode } from './rational.ts';
import { ROUNDING_MODES } from './rational.ts';
import { MigrationError } from './errors.ts';

export type SchemaNode =
  | { readonly type: 'object'; readonly fields: Record<string, SchemaNode> }
  | { readonly type: 'array'; readonly items: SchemaNode }
  | {
      readonly type: 'number';
      readonly unit: string;
      readonly integer?: boolean;
      readonly rounding?: RoundingMode;
    }
  | { readonly type: 'string' }
  | { readonly type: 'boolean' }
  | { readonly type: 'null' }
  | { readonly type: 'any' };

export type PathSegment = { readonly key: string } | { readonly anyIndex: true };

/**
 * Parse a schema path pattern like `readings[].value` or `meta.site`.
 * `[]` matches every array element (arrays are ordered; concrete paths
 * carry the real index, e.g. `readings[2].value`).
 */
export function parsePathPattern(pattern: string): PathSegment[] {
  if (pattern === '' ) throw new MigrationError('unknown-path', 'empty path pattern');
  const segments: PathSegment[] = [];
  const parts = pattern.split('.');
  for (const part of parts) {
    if (part === '') throw new MigrationError('unknown-path', `bad path pattern: ${pattern}`);
    const m = /^([A-Za-z0-9_-]+?)(\[\])?$/.exec(part);
    if (!m) throw new MigrationError('unknown-path', `bad path segment ${JSON.stringify(part)} in ${pattern}`);
    segments.push({ key: m[1]! });
    if (m[2] !== undefined) segments.push({ anyIndex: true });
  }
  return segments;
}

/** Format a concrete path from keys and array indices. */
export function formatConcretePath(segments: readonly (string | number)[]): string {
  let out = '';
  for (const seg of segments) {
    if (typeof seg === 'number') out += `[${seg}]`;
    else out += (out === '' ? '' : '.') + seg;
  }
  return out === '' ? '<root>' : out;
}

export interface NumberLeaf {
  readonly unit: string;
  readonly integer: boolean;
  readonly rounding?: RoundingMode;
}

/**
 * Resolve a parsed pattern against the schema. Returns the numeric leaf
 * descriptor. Throws MigrationError with the offending pattern otherwise.
 */
export function resolveNumberLeaf(
  schema: SchemaNode,
  segments: readonly PathSegment[],
  pattern: string,
): NumberLeaf {
  let node: SchemaNode = schema;
  for (const seg of segments) {
    if ('key' in seg) {
      if (node.type !== 'object' || !(seg.key in node.fields)) {
        throw new MigrationError('unknown-path', `path does not exist in schema`, pattern);
      }
      node = node.fields[seg.key]!;
    } else {
      if (node.type !== 'array') {
        throw new MigrationError(
          'path-type-mismatch',
          `expected array at [] segment but schema has ${node.type}`,
          pattern,
        );
      }
      node = node.items;
    }
  }
  if (node.type !== 'number') {
    throw new MigrationError(
      'path-type-mismatch',
      `path resolves to ${node.type}, not a numeric field`,
      pattern,
    );
  }
  if (node.rounding !== undefined && !ROUNDING_MODES.includes(node.rounding)) {
    throw new MigrationError(
      'missing-rounding-mode',
      `unknown rounding mode ${JSON.stringify(node.rounding)}`,
      pattern,
    );
  }
  return { unit: node.unit, integer: node.integer === true, rounding: node.rounding };
}
