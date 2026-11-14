import { isForbiddenKey } from './safe.ts';

export type TemplateFilter = (value: unknown, ...args: unknown[]) => unknown;

export interface RenderLimits {
  maxNodes?: number;
  maxDepth?: number;
  maxIterations?: number;
  maxOutputLength?: number;
  maxTemplateLength?: number;
  maxValueNodes?: number;
  maxValueStringLength?: number;
}

export interface RenderOptions extends RenderLimits {
  /** Root names that templates may reference. An object also supplies defaults. */
  variables?: readonly string[] | Readonly<Record<string, unknown>>;
  allowedVariables?: readonly string[];
  declaredVariables?: readonly string[];
  /** Optional full-path whitelist, e.g. `["user.name", "items.*.title"]`. */
  allowedPaths?: readonly string[];
  declaredPaths?: readonly string[];
  whitelist?: readonly string[];
  /** Host-registered filters. Context values can never supply filters. */
  filters?: Readonly<Record<string, TemplateFilter>>;
  filterMap?: Readonly<Record<string, TemplateFilter>>;
  allowedFilters?: readonly string[];
  strictVariables?: boolean;
  escapeHtml?: boolean;
  limits?: RenderLimits;
  nodeLimit?: number;
  maxAstNodes?: number;
  depthLimit?: number;
  maxLoops?: number;
  maxLoopIterations?: number;
  loopLimit?: number;
  maxOutput?: number;
  outputLength?: number;
  maxOutputChars?: number;
  maxTemplateSize?: number;
  templateLength?: number;
  valueNodeLimit?: number;
  maxValueLength?: number;
}

export interface NormalizedLimits {
  maxNodes: number;
  maxDepth: number;
  maxIterations: number;
  maxOutputLength: number;
  maxTemplateLength: number;
  maxValueNodes: number;
  maxValueStringLength: number;
}

export interface PathPattern {
  segments: string[];
}

export interface NormalizedOptions {
  limits: NormalizedLimits;
  declaredVariables: Set<string> | null;
  defaults: Record<string, unknown>;
  pathPatterns: PathPattern[] | null;
  filters: Map<string, TemplateFilter>;
  allowedFilters: Set<string> | null;
  strictVariables: boolean;
  escapeHtml: boolean;
}

const DEFAULT_LIMITS: NormalizedLimits = {
  maxNodes: 10_000,
  maxDepth: 64,
  maxIterations: 10_000,
  maxOutputLength: 100_000,
  maxTemplateLength: 1_000_000,
  maxValueNodes: 10_000,
  maxValueStringLength: 100_000,
};

const LIMIT_ALIASES: Record<keyof NormalizedLimits, readonly string[]> = {
  maxNodes: ['nodeLimit', 'maxAstNodes'],
  maxDepth: ['depthLimit'],
  maxIterations: ['maxLoops', 'maxLoopIterations', 'loopLimit'],
  maxOutputLength: ['maxOutput', 'outputLength', 'maxOutputChars'],
  maxTemplateLength: ['maxTemplateSize', 'templateLength'],
  maxValueNodes: ['valueNodeLimit'],
  maxValueStringLength: ['maxValueLength'],
};

function positiveInteger(value: unknown, fallback: number): number {
  if (
    typeof value === 'number' &&
    Number.isFinite(value) &&
    Number.isInteger(value) &&
    value > 0
  ) {
    return value;
  }
  return fallback;
}

function ownDataValue(source: object, key: string): unknown {
  try {
    const descriptor = Object.getOwnPropertyDescriptor(source, key);
    return descriptor !== undefined && 'value' in descriptor
      ? descriptor.value
      : undefined;
  } catch {
    return undefined;
  }
}

function addVariableNames(
  target: Set<string>,
  value: readonly string[] | Readonly<Record<string, unknown>> | undefined,
): void {
  if (Array.isArray(value)) {
    for (const name of value) {
      if (typeof name === 'string' && !isForbiddenKey(name)) {
        target.add(name);
      }
    }
    return;
  }
  if (value !== null && typeof value === 'object') {
    let names: string[];
    try {
      names = Object.getOwnPropertyNames(value);
    } catch {
      return;
    }
    for (const name of names) {
      if (!isForbiddenKey(name)) {
        target.add(name);
      }
    }
  }
}

function collectDefaults(value: unknown): Record<string, unknown> {
  const result: Record<string, unknown> = Object.create(null);
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return result;
  }

  let names: string[];
  try {
    names = Object.getOwnPropertyNames(value);
  } catch {
    return result;
  }

  for (const name of names) {
    if (isForbiddenKey(name)) {
      continue;
    }
    const descriptor = (() => {
      try {
        return Object.getOwnPropertyDescriptor(value, name);
      } catch {
        return undefined;
      }
    })();
    if (descriptor !== undefined && 'value' in descriptor) {
      result[name] = descriptor.value;
    }
  }
  return result;
}

function collectFilters(
  target: Map<string, TemplateFilter>,
  value: Readonly<Record<string, TemplateFilter>> | undefined,
): void {
  if (value === null || typeof value !== 'object') {
    return;
  }
  let names: string[];
  try {
    names = Object.getOwnPropertyNames(value);
  } catch {
    return;
  }
  for (const name of names) {
    if (isForbiddenKey(name)) {
      continue;
    }
    const candidate = ownDataValue(value, name);
    if (typeof candidate === 'function') {
      target.set(name, candidate as TemplateFilter);
    }
  }
}

export function parsePathPattern(text: string): PathPattern | null {
  if (typeof text !== 'string') {
    return null;
  }
  const segments = text
    .split(/[./]/)
    .map((segment) => segment.trim())
    .filter((segment) => segment.length > 0);
  if (segments.length === 0) {
    return null;
  }
  return { segments };
}

function collectPathPatterns(
  target: PathPattern[],
  value: readonly string[] | undefined,
): void {
  if (!Array.isArray(value)) {
    return;
  }
  for (const item of value) {
    if (typeof item !== 'string') {
      continue;
    }
    const pattern = parsePathPattern(item);
    if (pattern !== null) {
      target.push(pattern);
    }
  }
}

function patternMatchesPrefix(
  pattern: readonly string[],
  path: readonly string[],
): boolean {
  const length = Math.min(pattern.length, path.length);
  for (let index = 0; index < length; index += 1) {
    if (pattern[index] === '**') {
      return true;
    }
    if (pattern[index] !== '*' && pattern[index] !== path[index]) {
      return false;
    }
  }
  return true;
}

function patternMatchesFull(
  pattern: readonly string[],
  path: readonly string[],
): boolean {
  for (let index = 0; index < pattern.length; index += 1) {
    if (pattern[index] === '**') {
      return true;
    }
    if (index >= path.length) {
      return false;
    }
    if (pattern[index] !== '*' && pattern[index] !== path[index]) {
      return false;
    }
  }
  return pattern.length === path.length;
}

export function pathIsAllowed(
  patterns: readonly PathPattern[] | null,
  path: readonly string[],
): boolean {
  if (patterns === null) {
    return true;
  }
  for (const pattern of patterns) {
    if (
      (path.length <= pattern.segments.length &&
        patternMatchesPrefix(pattern.segments, path)) ||
      (path.length >= pattern.segments.length &&
        patternMatchesFull(pattern.segments, path))
    ) {
      return true;
    }
  }
  return false;
}

export function normalizeOptions(input: RenderOptions = {}): NormalizedOptions {
  const source: RenderOptions =
    input !== null && typeof input === 'object' ? input : {};
  const nested: RenderLimits =
    source.limits !== null && typeof source.limits === 'object'
      ? source.limits
      : {};

  const readLimit = (name: keyof NormalizedLimits): number => {
    const candidates: unknown[] = [source[name], nested[name]];
    for (const alias of LIMIT_ALIASES[name]) {
      candidates.push(
        (source as Record<string, unknown>)[alias],
        (nested as Record<string, unknown>)[alias],
      );
    }
    for (const candidate of candidates) {
      if (candidate !== undefined) {
        return positiveInteger(candidate, DEFAULT_LIMITS[name]);
      }
    }
    return DEFAULT_LIMITS[name];
  };

  const limits: NormalizedLimits = {
    maxNodes: readLimit('maxNodes'),
    maxDepth: readLimit('maxDepth'),
    maxIterations: readLimit('maxIterations'),
    maxOutputLength: readLimit('maxOutputLength'),
    maxTemplateLength: readLimit('maxTemplateLength'),
    maxValueNodes: readLimit('maxValueNodes'),
    maxValueStringLength: readLimit('maxValueStringLength'),
  };

  const declared = new Set<string>();
  addVariableNames(declared, source.variables);
  addVariableNames(declared, source.allowedVariables);
  addVariableNames(declared, source.declaredVariables);
  const hasDeclaredOption =
    source.variables !== undefined ||
    source.allowedVariables !== undefined ||
    source.declaredVariables !== undefined;

  const patternList: PathPattern[] = [];
  collectPathPatterns(patternList, source.allowedPaths);
  collectPathPatterns(patternList, source.declaredPaths);
  collectPathPatterns(patternList, source.whitelist);
  const hasPathOption =
    source.allowedPaths !== undefined ||
    source.declaredPaths !== undefined ||
    source.whitelist !== undefined;

  const filters = new Map<string, TemplateFilter>();
  collectFilters(filters, source.filters);
  collectFilters(filters, source.filterMap);

  let allowedFilters: Set<string> | null = null;
  if (Array.isArray(source.allowedFilters)) {
    allowedFilters = new Set(
      source.allowedFilters.filter(
        (name): name is string =>
          typeof name === 'string' && !isForbiddenKey(name),
      ),
    );
  }

  return {
    limits,
    declaredVariables: hasDeclaredOption ? declared : null,
    defaults: collectDefaults(source.variables),
    pathPatterns: hasPathOption ? patternList : null,
    filters,
    allowedFilters,
    strictVariables: source.strictVariables === true,
    escapeHtml: source.escapeHtml === true,
  };
}

export function mergeOptions(
  base: RenderOptions,
  overrides: RenderOptions | undefined,
): RenderOptions {
  if (overrides === undefined || overrides === null) {
    return base;
  }
  return {
    ...base,
    ...overrides,
    limits: {
      ...(base.limits ?? {}),
      ...(overrides.limits ?? {}),
    },
  };
}
