/**
 * The supported schema subset. Nothing outside of this is promised:
 *
 * - objects:  { type: 'object', properties?, required? }
 * - arrays:   { type: 'array', items }
 * - unions:   { oneOf: [...] }            (exactly one branch must match)
 * - refs:     { $ref: '#...' }            (local JSON-pointer refs only)
 * - scalars:  { type: 'string' | 'number' | 'boolean' | 'null' }
 */
export type ScalarType = 'string' | 'number' | 'boolean' | 'null';

export type Schema =
  | { type: 'object'; properties?: Record<string, Schema>; required?: string[] }
  | { type: 'array'; items: Schema }
  | { type: ScalarType }
  | { oneOf: Schema[] }
  | { $ref: string };
