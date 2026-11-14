import { TemplateError } from './errors.ts';
import type { SourceLocation } from './location.ts';
import {
  pathIsAllowed,
  type NormalizedLimits,
  type PathPattern,
} from './options.ts';
import {
  MISSING,
  UNSAFE,
  arrayLength,
  box,
  enumerableEntries,
  isArrayView,
  isObjectView,
  isPrimitiveView,
  isUnsupportedView,
  readProperty,
  toJson,
  toPrimitive,
  type SafeValue,
} from './safe.ts';

export interface BuiltinFilterContext {
  location: SourceLocation;
  limits: NormalizedLimits;
  pathPatterns?: readonly PathPattern[] | null;
  pathPrefix?: readonly string[];
}

export type BuiltinFilter = (
  value: SafeValue,
  args: SafeValue[],
  context: BuiltinFilterContext,
) => SafeValue;

function itemAt(value: SafeValue, index: number): SafeValue {
  const property = readProperty(value, String(index));
  return property === MISSING || property === UNSAFE
    ? box(undefined)
    : property;
}

function pathPrefix(context: BuiltinFilterContext): readonly string[] {
  return context.pathPrefix ?? [];
}

function pathAllowed(
  context: BuiltinFilterContext,
  path: readonly string[],
): boolean {
  const patterns = context.pathPatterns;
  return patterns === undefined || patterns === null
    ? true
    : pathIsAllowed(patterns, path);
}

function allowedEntries(
  value: SafeValue,
  context: BuiltinFilterContext,
): ReturnType<typeof enumerableEntries> {
  const entries = enumerableEntries(
    value,
    context.limits.maxValueNodes + 1,
  );
  if (entries.length > context.limits.maxValueNodes) {
    throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
  }
  const prefix = pathPrefix(context);
  return entries.filter((entry) =>
    pathAllowed(context, [...prefix, entry.key]),
  );
}

function asString(
  value: SafeValue,
  context?: BuiltinFilterContext,
  path?: readonly string[],
): string {
  if (isPrimitiveView(value)) {
    const primitive = toPrimitive(value);
    if (primitive === null || primitive === undefined) return '';
    const text = String(primitive);
    if (
      context !== undefined &&
      text.length > context.limits.maxValueStringLength
    ) {
      throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
    }
    return text;
  }
  if (isArrayView(value)) {
    const entries: string[] = [];
    let totalLength = 0;
    const length = arrayLength(value) ?? 0;
    if (
      context !== undefined &&
      length > context.limits.maxValueNodes
    ) {
      throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
    }
    const prefix = path ?? (context === undefined ? [] : pathPrefix(context));
    for (let index = 0; index < length; index += 1) {
      const itemPath = [...prefix, String(index)];
      if (
        context !== undefined &&
        !pathAllowed(context, itemPath)
      ) {
        continue;
      }
      const item = asString(itemAt(value, index), context, itemPath);
      totalLength += item.length + (entries.length === 0 ? 0 : 1);
      if (
        context !== undefined &&
        totalLength > context.limits.maxValueStringLength
      ) {
        throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
      }
      entries.push(item);
    }
    return entries.join(',');
  }
  return '';
}

function asNumber(value: SafeValue, fallback: number): number {
  if (!isPrimitiveView(value)) return fallback;
  const primitive = toPrimitive(value);
  if (typeof primitive === 'number' && Number.isFinite(primitive)) {
    return primitive;
  }
  if (typeof primitive === 'string') {
    const parsed = Number(primitive);
    return Number.isFinite(parsed) ? parsed : fallback;
  }
  return fallback;
}

function escaped(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

const builtins = new Map<string, BuiltinFilter>();

function define(name: string, filter: BuiltinFilter): void {
  builtins.set(name, filter);
}

define('upper', (value, _args, context) =>
  box(asString(value, context).toUpperCase()));
define('uppercase', (value, _args, context) =>
  box(asString(value, context).toUpperCase()));
define('lower', (value, _args, context) =>
  box(asString(value, context).toLowerCase()));
define('lowercase', (value, _args, context) =>
  box(asString(value, context).toLowerCase()));
define('trim', (value, _args, context) =>
  box(asString(value, context).trim()));
define('capitalize', (value, _args, context) => {
  const text = asString(value, context);
  return box(text.length === 0 ? '' : text[0].toUpperCase() + text.slice(1));
});
define('escape', (value, _args, context) =>
  box(escaped(asString(value, context))));
define('html', (value, _args, context) =>
  box(escaped(asString(value, context))));
define('urlencode', (value, _args, context) =>
  box(encodeURIComponent(asString(value, context))));

define('length', (value, _args, context) => {
  if (isArrayView(value)) return box(arrayLength(value) ?? 0);
  if (isObjectView(value)) {
    return box(allowedEntries(value, context).length);
  }
  if (isPrimitiveView(value)) {
    const primitive = toPrimitive(value);
    if (typeof primitive === 'string') return box(primitive.length);
  }
  return box(0);
});

define('default', (value, args) => {
  let empty = false;
  if (isUnsupportedView(value)) {
    empty = true;
  } else if (isPrimitiveView(value)) {
    const primitive = toPrimitive(value);
    empty = primitive === null || primitive === undefined || primitive === '';
  } else if (isArrayView(value)) {
    empty = (arrayLength(value) ?? 0) === 0;
  }
  if (empty) {
    return args.length > 0 ? args[0] : box('');
  }
  return value;
});

define('json', (value, _args, context) =>
  box(
    toJson(value, {
      maxLength: context.limits.maxValueStringLength,
      maxDepth: context.limits.maxDepth,
      maxNodes: context.limits.maxValueNodes,
      pathAllowed:
        context.pathPatterns === undefined || context.pathPatterns === null
          ? undefined
          : (path) => pathIsAllowed(context.pathPatterns ?? null, path),
    }, context.location, context.pathPrefix ?? []),
  ),
);

define('truncate', (value, args, context) => {
  const text = asString(value, context);
  const width = args.length > 0
    ? Math.max(0, Math.floor(asNumber(args[0], text.length)))
    : text.length;
  if (text.length <= width) return box(text);
  const suffix = args.length > 1 ? asString(args[1], context) : '';
  const keep = Math.max(0, width - suffix.length);
  return box(text.slice(0, keep) + suffix);
});

define('replace', (value, args, context) => {
  const text = asString(value, context);
  const search = asString(args[0] ?? box(''), context);
  const replacement = asString(args[1] ?? box(''), context);
  if (search.length === 0) return box(text);
  let occurrences = 0;
  let cursor = 0;
  while (cursor <= text.length - search.length) {
    const found = text.indexOf(search, cursor);
    if (found === -1) break;
    occurrences += 1;
    cursor = found + search.length;
  }
  const maximum = Math.min(
    context.limits.maxValueStringLength,
    context.limits.maxOutputLength,
  );
  const outputLength =
    text.length + occurrences * (replacement.length - search.length);
  if (outputLength > maximum) {
    throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
  }
  return box(text.split(search).join(replacement));
});

define('join', (value, args, context) => {
  const separator = args.length > 0 ? asString(args[0], context) : ',';
  if (!isArrayView(value)) return box(asString(value, context));
  const length = arrayLength(value) ?? 0;
  if (length > context.limits.maxValueNodes) {
    throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
  }
  const parts: string[] = [];
  let totalLength = 0;
  const prefix = pathPrefix(context);
  for (let index = 0; index < length; index += 1) {
    const itemPath = [...prefix, String(index)];
    if (!pathAllowed(context, itemPath)) {
      continue;
    }
    const part = asString(itemAt(value, index), context);
    totalLength += part.length + (parts.length === 0 ? 0 : separator.length);
    if (totalLength > context.limits.maxOutputLength) {
      throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
    }
    parts.push(part);
  }
  return box(parts.join(separator));
});

define('keys', (value, _args, context) => {
  if (!isObjectView(value)) return box([] as unknown[]);
  return box(allowedEntries(value, context).map((entry) => entry.key));
});

define('values', (value, _args, context) => {
  if (!isObjectView(value)) return box([] as unknown[]);
  return box(allowedEntries(value, context).map((entry) => entry.value));
});

define('slice', (value, args, context) => {
  if (isArrayView(value)) {
    const length = arrayLength(value) ?? 0;
    const start = Math.max(0, Math.floor(asNumber(args[0] ?? box(0), 0)));
    const end = args.length > 1
      ? Math.max(start, Math.floor(asNumber(args[1], length)))
      : length;
    if (end - start > context.limits.maxValueNodes) {
      throw new TemplateError(context.location, 'BUDGET_EXCEEDED');
    }
    const items: unknown[] = [];
    const prefix = pathPrefix(context);
    for (let index = start; index < Math.min(end, length); index += 1) {
      const itemPath = [...prefix, String(index)];
      if (!pathAllowed(context, itemPath)) {
        items.push(undefined);
        continue;
      }
      items.push(itemAt(value, index));
    }
    return box(items);
  }
  const text = asString(value, context);
  const start = Math.max(0, Math.floor(asNumber(args[0] ?? box(0), 0)));
  const end = args.length > 1
    ? Math.max(start, Math.floor(asNumber(args[1], text.length)))
    : text.length;
  return box(text.slice(start, end));
});

define('bool', (value) => {
  if (!isPrimitiveView(value) || isUnsupportedView(value)) return box(false);
  return box(Boolean(toPrimitive(value)));
});

export function getBuiltinFilter(name: string): BuiltinFilter | undefined {
  return builtins.get(name);
}

export function builtinFilterNames(): string[] {
  return [...builtins.keys()];
}
