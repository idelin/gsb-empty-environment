import {
  Json,
  PathSegment,
  Schema,
  ValidateOptions,
  ValidationError,
  ValidationResult,
  BranchFailure,
} from './schema';

interface Ctx {
  root: Schema;
  maxDepth: number;
  active: Set<string>;
  errors: ValidationError[];
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

function unescapePointer(segment: string): string {
  return segment.replace(/~1/g, '/').replace(/~0/g, '~');
}

function resolveRef(root: Schema, ref: string): { schema: Schema; ptr: string } | undefined {
  if (ref === '#') {
    return { schema: root, ptr: '' };
  }
  if (!ref.startsWith('#/')) {
    return undefined;
  }
  const parts = ref.slice(2).split('/').map(unescapePointer);
  let node: unknown = root;
  for (const part of parts) {
    if (typeof node !== 'object' || node === null || !(part in node)) {
      return undefined;
    }
    node = (node as Record<string, unknown>)[part];
  }
  return { schema: node as Schema, ptr: ref.slice(1) };
}

function pairKey(path: PathSegment[], ptr: string): string {
  return JSON.stringify(path) + ' ' + ptr;
}

function compareSegments(a: PathSegment, b: PathSegment): number {
  if (typeof a === 'number' && typeof b === 'number') {
    return a - b;
  }
  if (typeof a === 'number') {
    return -1;
  }
  if (typeof b === 'number') {
    return 1;
  }
  return a < b ? -1 : a > b ? 1 : 0;
}

export function comparePaths(a: PathSegment[], b: PathSegment[]): number {
  if (a.length !== b.length) {
    return a.length - b.length;
  }
  for (let i = 0; i < a.length; i++) {
    const c = compareSegments(a[i], b[i]);
    if (c !== 0) {
      return c;
    }
  }
  return 0;
}

export function bestError(errors: ValidationError[]): ValidationError {
  let best = errors[0];
  for (let i = 1; i < errors.length; i++) {
    if (comparePaths(errors[i].path, best.path) < 0) {
      best = errors[i];
    }
  }
  return best;
}

export function formatPath(path: PathSegment[]): string {
  let out = '$';
  for (const seg of path) {
    if (typeof seg === 'number') {
      out += '[' + seg + ']';
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) {
      out += '.' + seg;
    } else {
      out += '[' + JSON.stringify(seg) + ']';
    }
  }
  return out;
}

function fail(ctx: Ctx, path: PathSegment[], message: string, branches?: BranchFailure[]): false {
  const error: ValidationError = { path: path.slice(), message };
  if (branches !== undefined) {
    error.branches = branches;
  }
  ctx.errors.push(error);
  return false;
}

function isPlainObject(value: Json): value is { [key: string]: Json } {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function validateNode(
  instance: Json,
  schema: Schema,
  ctx: Ctx,
  path: PathSegment[],
  ptr: string,
  depth: number,
): boolean {
  if (depth > ctx.maxDepth) {
    return fail(
      ctx,
      path,
      'instance depth ' + depth + ' exceeds budget ' + ctx.maxDepth + ' at ' + formatPath(path),
    );
  }

  if ('$ref' in schema) {
    const resolved = resolveRef(ctx.root, schema.$ref);
    if (resolved === undefined) {
      return fail(ctx, path, 'unresolvable $ref "' + schema.$ref + '" at ' + formatPath(path));
    }
    const key = pairKey(path, resolved.ptr);
    if (ctx.active.has(key)) {
      return true;
    }
    ctx.active.add(key);
    try {
      return validateNode(instance, resolved.schema, ctx, path, resolved.ptr, depth);
    } finally {
      ctx.active.delete(key);
    }
  }

  if ('oneOf' in schema) {
    const matches: number[] = [];
    const failures: BranchFailure[] = [];
    for (let i = 0; i < schema.oneOf.length; i++) {
      const branchCtx: Ctx = {
        root: ctx.root,
        maxDepth: ctx.maxDepth,
        active: ctx.active,
        errors: [],
      };
      const ok = validateNode(instance, schema.oneOf[i], branchCtx, path, ptr + '/oneOf/' + i, depth);
      if (ok) {
        matches.push(i);
      } else {
        const branchErr =
          branchCtx.errors.length > 0
            ? bestError(branchCtx.errors)
            : { path: path.slice(), message: 'branch ' + i + ' failed' };
        failures.push({ branch: i, error: branchErr });
      }
    }
    if (matches.length === 1) {
      return true;
    }
    if (matches.length === 0) {
      return fail(
        ctx,
        path,
        'instance matches none of the ' + schema.oneOf.length + ' oneOf branches at ' + formatPath(path),
        failures,
      );
    }
    return fail(
      ctx,
      path,
      'instance matches ' + matches.length + ' oneOf branches (' + matches.join(', ') + '), expected exactly 1, at ' + formatPath(path),
    );
  }

  switch (schema.type) {
    case 'null':
      if (instance !== null) {
        return fail(ctx, path, 'expected null at ' + formatPath(path));
      }
      return true;
    case 'boolean':
      if (typeof instance !== 'boolean') {
        return fail(ctx, path, 'expected boolean at ' + formatPath(path));
      }
      return true;
    case 'number':
      if (typeof instance !== 'number') {
        return fail(ctx, path, 'expected number at ' + formatPath(path));
      }
      return true;
    case 'integer':
      if (typeof instance !== 'number' || !Number.isInteger(instance)) {
        return fail(ctx, path, 'expected integer at ' + formatPath(path));
      }
      return true;
    case 'string':
      if (typeof instance !== 'string') {
        return fail(ctx, path, 'expected string at ' + formatPath(path));
      }
      return true;
    case 'array': {
      if (!Array.isArray(instance)) {
        return fail(ctx, path, 'expected array at ' + formatPath(path));
      }
      if (schema.minItems !== undefined && instance.length < schema.minItems) {
        return fail(
          ctx,
          path,
          'array length ' + instance.length + ' < minItems ' + schema.minItems + ' at ' + formatPath(path),
        );
      }
      if (schema.maxItems !== undefined && instance.length > schema.maxItems) {
        return fail(
          ctx,
          path,
          'array length ' + instance.length + ' > maxItems ' + schema.maxItems + ' at ' + formatPath(path),
        );
      }
      if (schema.items !== undefined) {
        let ok = true;
        for (let i = 0; i < instance.length; i++) {
          if (!validateNode(instance[i], schema.items, ctx, path.concat(i), ptr + '/items', depth + 1)) {
            ok = false;
          }
        }
        return ok;
      }
      return true;
    }
    case 'object': {
      if (!isPlainObject(instance)) {
        return fail(ctx, path, 'expected object at ' + formatPath(path));
      }
      let ok = true;
      if (schema.required !== undefined) {
        for (const key of schema.required) {
          if (!(key in instance)) {
            fail(ctx, path, 'missing required property "' + key + '" at ' + formatPath(path));
            ok = false;
          }
        }
      }
      if (schema.properties !== undefined) {
        for (const key of Object.keys(schema.properties)) {
          if (key in instance) {
            const childPtr = ptr + '/properties/' + escapePointer(key);
            if (!validateNode(instance[key], schema.properties[key], ctx, path.concat(key), childPtr, depth + 1)) {
              ok = false;
            }
          }
        }
      }
      return ok;
    }
  }
}

export function validate(instance: Json, schema: Schema, options: ValidateOptions = {}): ValidationResult {
  const ctx: Ctx = {
    root: schema,
    maxDepth: options.maxDepth === undefined ? Infinity : options.maxDepth,
    active: new Set<string>(),
    errors: [],
  };
  const ok = validateNode(instance, schema, ctx, [], '', 0);
  if (ok) {
    return { valid: true };
  }
  return { valid: false, error: bestError(ctx.errors) };
}
