/**
 * A dependency-free JSON Schema *subset* validator with a shared work budget.
 *
 * Implemented schema subset (anything else is ignored, never promised):
 *   - type (string | number | integer | boolean | null | array | object, or an array of these)
 *   - properties, required, additionalProperties (boolean or schema)
 *   - items (single schema), minItems, maxItems
 *   - minLength, maxLength, pattern
 *   - minimum, maximum, exclusiveMinimum, exclusiveMaximum
 *   - enum, const
 *   - allOf, anyOf
 *   - $ref (local JSON Pointers only: "#", "#/...", URL-decoded, ~0/~1 unescaped)
 *
 * Budget model (one shared counter per validate() call):
 *   - visiting a schema node / dispatching a keyword costs budget (parsing share)
 *   - resolving a $ref costs REF_COST, even when the target is cached, so
 *     large numbers of duplicate references cannot bypass the budget
 *   - pattern matching costs 2 + string length (string-matching share)
 *   - deep equality (enum/const) costs per compared node
 *   - recording an error costs budget (error-collection share)
 *
 * Outcomes:
 *   - 'valid'       : instance conforms to the implemented subset
 *   - 'invalid'     : at least one violation, fully reported within limits
 *   - 'incomplete'  : budget exhausted / cancelled / error limit / depth limit
 *                     -- the instance was NOT proven invalid; the run just stopped
 *
 * Every validate() call builds fresh internal state; nothing (including an
 * exhausted budget) leaks into the next call. Temporary caches are released
 * in a finally block on every exit path.
 */

export type ValidationStatus = 'valid' | 'invalid' | 'incomplete';

export type IncompleteReason =
  | 'budget-exhausted'
  | 'cancelled'
  | 'error-limit-reached'
  | 'depth-limit-reached';

export interface ValidationError {
  /** JSON Pointer into the instance, e.g. "/users/0/name". */
  instancePath: string;
  /** JSON Pointer into the schema of the failing rule, e.g. "/properties/name/minLength". */
  schemaPath: string;
  message: string;
}

export interface ValidateOptions {
  /** Total shared budget units for this run. Default 10_000. */
  budget?: number;
  /** Stop after this many errors and report 'incomplete'. Default 100. */
  maxErrors?: number;
  /** Maximum schema recursion depth. Default 100. */
  maxDepth?: number;
  /** AbortSignal checked at every budget checkpoint. */
  signal?: AbortSignal;
}

export interface ValidationResult {
  status: ValidationStatus;
  /** Present iff status === 'incomplete'. */
  reason?: IncompleteReason;
  errors: ValidationError[];
  budgetLimit: number;
  budgetUsed: number;
}

const REF_COST = 5;
const PATTERN_BASE_COST = 2;

class BudgetExhausted extends Error {}
class Cancelled extends Error {}
class ErrorLimitReached extends Error {}
class DepthLimitReached extends Error {}

interface Ctx {
  limit: number;
  used: number;
  maxErrors: number;
  maxDepth: number;
  signal: AbortSignal | undefined;
  errors: ValidationError[];
  rootSchema: unknown;
  /** Temporary per-run state, always nulled in finally. */
  refCache: Map<string, unknown> | null;
  refStack: string[] | null;
}

function spend(ctx: Ctx, cost: number): void {
  if (ctx.signal?.aborted) throw new Cancelled();
  ctx.used += cost;
  if (ctx.used > ctx.limit) throw new BudgetExhausted();
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

function joinPointer(base: string, segment: string): string {
  return base + '/' + escapePointer(segment);
}

function addError(ctx: Ctx, instancePath: string, schemaPath: string, message: string): void {
  spend(ctx, 1); // error collection shares the budget
  ctx.errors.push({ instancePath, schemaPath, message });
  if (ctx.errors.length >= ctx.maxErrors) throw new ErrorLimitReached();
}

function typeOf(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'number' && Number.isInteger(value)) return 'integer';
  return typeof value;
}

function matchesType(value: unknown, expected: string): boolean {
  const actual = typeOf(value);
  if (expected === 'integer') return actual === 'integer';
  if (expected === 'number') return actual === 'number' || actual === 'integer';
  return actual === expected;
}

function deepEqual(ctx: Ctx, a: unknown, b: unknown): boolean {
  spend(ctx, 1);
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!deepEqual(ctx, a[i], b[i])) return false;
    }
    return true;
  }
  const aObj = a as Record<string, unknown>;
  const bObj = b as Record<string, unknown>;
  const aKeys = Object.keys(aObj);
  const bKeys = Object.keys(bObj);
  if (aKeys.length !== bKeys.length) return false;
  for (const key of aKeys) {
    if (!Object.prototype.hasOwnProperty.call(bObj, key)) return false;
    if (!deepEqual(ctx, aObj[key], bObj[key])) return false;
  }
  return true;
}

function resolvePointer(pointer: string, root: unknown): unknown {
  if (pointer === '#') return root;
  if (!pointer.startsWith('#/')) return undefined;
  let current: unknown = root;
  const segments = pointer
    .slice(2)
    .split('/')
    .map((s) => decodeURIComponent(s).replace(/~1/g, '/').replace(/~0/g, '~'));
  for (const segment of segments) {
    if (typeof current !== 'object' || current === null) return undefined;
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

function resolveRef(ctx: Ctx, ref: string): { schema: unknown; path: string } | undefined {
  spend(ctx, REF_COST); // charged per reference, cache hit or not
  const stack = ctx.refStack!;
  if (stack.includes(ref)) return undefined; // circular; caller reports it
  let target: unknown;
  if (ctx.refCache!.has(ref)) {
    target = ctx.refCache!.get(ref);
  } else {
    target = resolvePointer(ref, ctx.rootSchema);
    ctx.refCache!.set(ref, target);
  }
  if (target === undefined) return undefined;
  const path = ref === '#' ? '' : ref.slice(1);
  return { schema: target, path };
}

function checkPattern(ctx: Ctx, pattern: string, value: string): boolean | 'bad-pattern' {
  spend(ctx, PATTERN_BASE_COST + value.length); // string matching shares the budget
  let regex: RegExp;
  try {
    regex = new RegExp(pattern);
  } catch {
    return 'bad-pattern';
  }
  return regex.test(value);
}

function validateNode(
  ctx: Ctx,
  schema: unknown,
  instance: unknown,
  instancePath: string,
  schemaPath: string,
  depth: number,
): void {
  spend(ctx, 1); // schema parsing / node visit shares the budget
  if (depth > ctx.maxDepth) throw new DepthLimitReached();

  if (typeof schema === 'boolean') {
    if (!schema) addError(ctx, instancePath, schemaPath, 'boolean schema "false" rejects every instance');
    return;
  }
  if (typeof schema !== 'object' || schema === null || Array.isArray(schema)) {
    addError(ctx, instancePath, schemaPath, 'schema must be an object or boolean');
    return;
  }
  const s = schema as Record<string, unknown>;

  if (typeof s.$ref === 'string') {
    const ref = s.$ref;
    if (ctx.refStack!.includes(ref)) {
      addError(ctx, instancePath, joinPointer(schemaPath, '$ref'), `circular $ref "${ref}"`);
      return;
    }
    const resolved = resolveRef(ctx, ref);
    if (!resolved) {
      addError(ctx, instancePath, joinPointer(schemaPath, '$ref'), `unresolvable $ref "${ref}"`);
      return;
    }
    ctx.refStack!.push(ref);
    try {
      validateNode(ctx, resolved.schema, instance, instancePath, resolved.path, depth + 1);
    } finally {
      ctx.refStack!.pop();
    }
    return;
  }

  if (s.type !== undefined) {
    spend(ctx, 1);
    const allowed = Array.isArray(s.type) ? s.type : [s.type];
    const ok = allowed.some((t) => typeof t === 'string' && matchesType(instance, t));
    if (!ok) {
      addError(
        ctx,
        instancePath,
        joinPointer(schemaPath, 'type'),
        `expected type ${allowed.join(' | ')}, got ${typeOf(instance)}`,
      );
      return; // avoid cascading noise from further keyword checks
    }
  }

  if (s.const !== undefined) {
    spend(ctx, 1);
    if (!deepEqual(ctx, instance, s.const)) {
      addError(ctx, instancePath, joinPointer(schemaPath, 'const'), 'value does not equal const');
    }
  }

  if (Array.isArray(s.enum)) {
    spend(ctx, 1);
    const found = s.enum.some((candidate) => deepEqual(ctx, instance, candidate));
    if (!found) {
      addError(ctx, instancePath, joinPointer(schemaPath, 'enum'), 'value is not one of the enum values');
    }
  }

  if (typeof instance === 'string') {
    if (typeof s.minLength === 'number') {
      spend(ctx, 1);
      if (instance.length < s.minLength) {
        addError(ctx, instancePath, joinPointer(schemaPath, 'minLength'), `string shorter than ${s.minLength}`);
      }
    }
    if (typeof s.maxLength === 'number') {
      spend(ctx, 1);
      if (instance.length > s.maxLength) {
        addError(ctx, instancePath, joinPointer(schemaPath, 'maxLength'), `string longer than ${s.maxLength}`);
      }
    }
    if (typeof s.pattern === 'string') {
      const result = checkPattern(ctx, s.pattern, instance);
      if (result === 'bad-pattern') {
        addError(ctx, instancePath, joinPointer(schemaPath, 'pattern'), `invalid pattern "${s.pattern}"`);
      } else if (!result) {
        addError(ctx, instancePath, joinPointer(schemaPath, 'pattern'), `string does not match pattern "${s.pattern}"`);
      }
    }
  }

  if (typeof instance === 'number') {
    const numericChecks: Array<[string, (n: number, limit: number) => boolean, string]> = [
      ['minimum', (n, l) => n >= l, 'less than'],
      ['maximum', (n, l) => n <= l, 'greater than'],
      ['exclusiveMinimum', (n, l) => n > l, 'not greater than'],
      ['exclusiveMaximum', (n, l) => n < l, 'not less than'],
    ];
    for (const [keyword, ok, wording] of numericChecks) {
      const limit = s[keyword];
      if (typeof limit === 'number') {
        spend(ctx, 1);
        if (!ok(instance, limit)) {
          addError(ctx, instancePath, joinPointer(schemaPath, keyword), `${instance} is ${wording} ${keyword} ${limit}`);
        }
      }
    }
  }

  if (Array.isArray(instance)) {
    if (typeof s.minItems === 'number') {
      spend(ctx, 1);
      if (instance.length < s.minItems) {
        addError(ctx, instancePath, joinPointer(schemaPath, 'minItems'), `array has fewer than ${s.minItems} items`);
      }
    }
    if (typeof s.maxItems === 'number') {
      spend(ctx, 1);
      if (instance.length > s.maxItems) {
        addError(ctx, instancePath, joinPointer(schemaPath, 'maxItems'), `array has more than ${s.maxItems} items`);
      }
    }
    if (s.items !== undefined) {
      spend(ctx, 1);
      for (let i = 0; i < instance.length; i++) {
        validateNode(ctx, s.items, instance[i], joinPointer(instancePath, String(i)), joinPointer(schemaPath, 'items'), depth + 1);
      }
    }
  }

  if (typeOf(instance) === 'object') {
    const obj = instance as Record<string, unknown>;
    if (Array.isArray(s.required)) {
      spend(ctx, 1);
      for (const key of s.required) {
        if (typeof key === 'string' && !Object.prototype.hasOwnProperty.call(obj, key)) {
          addError(ctx, instancePath, joinPointer(schemaPath, 'required'), `missing required property "${key}"`);
        }
      }
    }
    if (typeof s.properties === 'object' && s.properties !== null) {
      spend(ctx, 1);
      for (const [key, subschema] of Object.entries(s.properties as Record<string, unknown>)) {
        if (Object.prototype.hasOwnProperty.call(obj, key)) {
          validateNode(ctx, subschema, obj[key], joinPointer(instancePath, key), joinPointer(joinPointer(schemaPath, 'properties'), key), depth + 1);
        }
      }
    }
    if (s.additionalProperties !== undefined) {
      spend(ctx, 1);
      const declared = new Set(
        typeof s.properties === 'object' && s.properties !== null ? Object.keys(s.properties) : [],
      );
      for (const key of Object.keys(obj)) {
        if (declared.has(key)) continue;
        if (s.additionalProperties === false) {
          addError(ctx, joinPointer(instancePath, key), joinPointer(schemaPath, 'additionalProperties'), `additional property "${key}" is not allowed`);
        } else if (typeof s.additionalProperties === 'object' || typeof s.additionalProperties === 'boolean') {
          validateNode(ctx, s.additionalProperties, obj[key], joinPointer(instancePath, key), joinPointer(schemaPath, 'additionalProperties'), depth + 1);
        }
      }
    }
  }

  if (Array.isArray(s.allOf)) {
    spend(ctx, 1);
    for (let i = 0; i < s.allOf.length; i++) {
      validateNode(ctx, s.allOf[i], instance, instancePath, joinPointer(joinPointer(schemaPath, 'allOf'), String(i)), depth + 1);
    }
  }

  if (Array.isArray(s.anyOf)) {
    spend(ctx, 1);
    for (let i = 0; i < s.anyOf.length; i++) {
      // anyOf: probe each branch against a scratch error list.
      const scratch: ValidationError[] = [];
      const saved = ctx.errors;
      ctx.errors = scratch;
      try {
        validateNode(ctx, s.anyOf[i], instance, instancePath, joinPointer(joinPointer(schemaPath, 'anyOf'), String(i)), depth + 1);
      } catch (err) {
        ctx.errors = saved;
        if (err instanceof ErrorLimitReached) {
          // scratch list hit the cap; treat as branch failure and keep probing
        } else {
          throw err;
        }
      }
      ctx.errors = saved;
      if (scratch.length === 0) return; // one branch matched: anyOf satisfied
    }
    addError(ctx, instancePath, joinPointer(schemaPath, 'anyOf'), 'value does not match any anyOf branch');
  }
}

export function validate(schema: unknown, instance: unknown, options: ValidateOptions = {}): ValidationResult {
  const ctx: Ctx = {
    limit: options.budget ?? 10_000,
    used: 0,
    maxErrors: options.maxErrors ?? 100,
    maxDepth: options.maxDepth ?? 100,
    signal: options.signal,
    errors: [],
    rootSchema: schema,
    refCache: new Map(),
    refStack: [],
  };

  let status: ValidationStatus = 'valid';
  let reason: IncompleteReason | undefined;
  try {
    validateNode(ctx, schema, instance, '', '', 0);
    if (ctx.errors.length > 0) status = 'invalid';
  } catch (err) {
    status = 'incomplete';
    if (err instanceof BudgetExhausted) reason = 'budget-exhausted';
    else if (err instanceof Cancelled) reason = 'cancelled';
    else if (err instanceof ErrorLimitReached) reason = 'error-limit-reached';
    else if (err instanceof DepthLimitReached) reason = 'depth-limit-reached';
    else throw err;
  } finally {
    // release temporary per-run state on every exit path
    ctx.refCache = null;
    ctx.refStack = null;
  }

  return {
    status,
    ...(reason ? { reason } : {}),
    errors: ctx.errors,
    budgetLimit: ctx.limit,
    budgetUsed: Math.min(ctx.used, ctx.limit),
  };
}
