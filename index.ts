export type AnnotationValueType = 'token' | 'size' | 'path';

export interface DirectiveTemplate {
  annotation?: string;
  annotationKey?: string;
  annotationName?: string;
  key?: string;
  directive?: string;
  directiveName?: string;
  target?: string;
  instruction?: string;
  name?: string;
  type?: string;
  valueType?: string;
  annotationType?: string;
  kind?: string;
  valueKind?: string;
  argType?: string;
  parameterType?: string;
  template?: string;
  format?: string;
  directiveTemplate?: string;
  pattern?: string;
  placeholder?: string;
  valuePlaceholder?: string;
  render?: (value: string, annotation?: string) => string;
  build?: (value: string, annotation?: string) => string;
}

export interface NormalizedDirectiveTemplate {
  annotation: string;
  directive?: string;
  type: AnnotationValueType;
  template?: string;
  placeholder?: string;
  render?: (value: string, annotation?: string) => string;
}

export type DirectiveTemplates =
  | readonly unknown[]
  | Map<unknown, unknown>
  | DirectiveTemplate
  | Record<string, unknown>
  | { templates?: readonly unknown[] | Record<string, unknown> };

export type AllowedDirectives =
  | readonly string[]
  | Set<string>
  | string
  | {
      allowedDirectives?: readonly string[] | Set<string> | string;
      whitelist?: readonly string[] | Set<string> | string;
      allowed?: readonly string[] | Set<string> | string;
      directives?: readonly string[] | Set<string> | string;
    };

export interface DroppedAnnotation {
  key: string;
  value?: unknown;
  reason: 'unknown-annotation' | 'invalid-value' | 'unknown-policy';
  message: string;
}

export interface RenderLogEvent {
  type:
    | 'unknown-annotation'
    | 'invalid-annotation'
    | 'duplicate-template'
    | 'dropped-annotation';
  level: 'warn' | 'error';
  key?: string;
  value?: unknown;
  message: string;
}

export type RenderLogger =
  | ((message: string, event: RenderLogEvent) => void)
  | false
  | null;

export interface RenderReport {
  rendered: string[];
  dropped: DroppedAnnotation[];
  directives: string[];
  unknownKeys: string[];
  warnings?: string[];
}

export interface RenderOptions {
  allowedDirectives?: readonly string[] | Set<string> | string;
  whitelist?: readonly string[] | Set<string> | string;
  allowed?: readonly string[] | Set<string> | string;
  directiveWhitelist?: readonly string[] | Set<string> | string;

  baseConfig?: string;
  base?: string;
  header?: string;
  prefix?: string;
  separator?: string;
  trailingNewline?: boolean;

  validateConfig?: boolean;
  skipConfigValidation?: boolean;

  strict?: boolean;
  invalidValuePolicy?: 'throw' | 'drop';
  unknownAnnotationPolicy?: 'drop' | 'throw';

  logger?: RenderLogger;
  onUnknownAnnotation?: (key: string, event: RenderLogEvent) => void;
  onUnknown?: (key: string, event: RenderLogEvent) => void;
  onInvalidAnnotation?: (key: string, message: string) => void;
  onInvalid?: (key: string, message: string) => void;
  onDrop?: (drop: DroppedAnnotation, event: RenderLogEvent) => void;

  report?: RenderReport;
  dropped?: DroppedAnnotation[];
  warnings?: string[];
  unknownKeys?: string[];
  log?: string[];
}

export interface ConfigInspection {
  ok: boolean;
  valid: boolean;
  errors: string[];
  directives: string[];
  disallowedDirectives: string[];
  braceDepth: number;
  depth: number;
  blockDepth: number;
  parenDepth: number;
  parenthesisDepth: number;
  statements: number;
}

export interface RenderResult {
  config: string;
  report: RenderReport;
  dropped: string[];
  droppedDetails: DroppedAnnotation[];
  droppedKeys: string[];
  directives: string[];
  warnings: string[];
  unknownKeys: string[];
  rendered: string[];
  errors: string[];
  inspection: ConfigInspection;
}

export class AnnotationValidationError extends Error {
  annotation: string;
  valueType: string;
  code: string;

  constructor(message: string, annotation: string, valueType: string) {
    super(message);
    this.name = 'AnnotationValidationError';
    this.annotation = annotation;
    this.valueType = valueType;
    this.code = 'INVALID_ANNOTATION_VALUE';
  }
}

export class TemplateValidationError extends Error {
  code: string;

  constructor(message: string) {
    super(message);
    this.name = 'TemplateValidationError';
    this.code = 'INVALID_DIRECTIVE_TEMPLATE';
  }
}

export class UnknownAnnotationError extends Error {
  annotation: string;
  code: string;

  constructor(message: string, annotation: string) {
    super(message);
    this.name = 'UnknownAnnotationError';
    this.annotation = annotation;
    this.code = 'UNKNOWN_ANNOTATION';
  }
}

export class ConfigValidationError extends Error {
  inspection: ConfigInspection;
  code: string;

  constructor(message: string, inspection: ConfigInspection) {
    super(message);
    this.name = 'ConfigValidationError';
    this.inspection = inspection;
    this.code = 'INVALID_RENDERED_CONFIG';
  }
}

const TOKEN_PATTERN = /^[A-Za-z0-9._-]+$/;
const SIZE_PATTERN = /^[0-9]+(?:k|m)?$/;
const DIRECTIVE_PATTERN = /^[A-Za-z0-9_.-]+$/;
const FORBIDDEN_VALUE_CHARS: ReadonlyArray<readonly [string, string]> = [
  ['\n', 'newline'],
  ['\r', 'carriage return'],
  ['\u2028', 'Unicode line separator'],
  ['\u2029', 'Unicode paragraph separator'],
  ['\u0000', 'NUL'],
  [';', 'semicolon'],
  ['{', 'left brace'],
  ['}', 'right brace'],
  ['`', 'backtick'],
  ['$', 'dollar sign'],
];

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === 'string' && value.trim() !== '') return value;
  }
  return undefined;
}

export function normalizeAnnotationValueType(
  type: unknown,
): AnnotationValueType | null {
  if (typeof type !== 'string') return null;
  switch (type.trim().toLowerCase()) {
    case 'token':
    case 'string':
    case 'name':
      return 'token';
    case 'size':
    case 'bytes':
    case 'byte-size':
      return 'size';
    case 'path':
    case 'filepath':
    case 'file-path':
    case 'uri':
      return 'path';
    default:
      return null;
  }
}

function coerceAnnotationValue(value: unknown): string | null {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  if (typeof value === 'bigint') return value.toString();
  return null;
}

export function getAnnotationValueError(
  value: unknown,
  type: unknown,
): string | null {
  const normalizedType = normalizeAnnotationValueType(type);
  if (normalizedType === null) {
    return `unsupported annotation value type "${String(type)}"`;
  }

  const text = coerceAnnotationValue(value);
  if (text === null) {
    return 'value must be a string or a finite number';
  }

  for (const [character, label] of FORBIDDEN_VALUE_CHARS) {
    if (text.includes(character)) {
      return `must not contain ${label} (${JSON.stringify(character)})`;
    }
  }
  if (/^\s*#/.test(text)) {
    return 'must not start with "#"';
  }

  if (normalizedType === 'token') {
    if (!TOKEN_PATTERN.test(text)) {
      return 'token values may only contain [A-Za-z0-9._-]';
    }
    return null;
  }

  if (normalizedType === 'size') {
    if (!SIZE_PATTERN.test(text)) {
      return 'size values must be digits followed by an optional "k" or "m" suffix';
    }
    return null;
  }

  if (!text.startsWith('/')) {
    return 'path values must start with "/"';
  }
  if (text.split('/').some((segment) => segment === '..')) {
    return 'path values must not contain a ".." segment';
  }
  return null;
}

export function validateAnnotationValue(value: unknown, type: unknown): boolean {
  return getAnnotationValueError(value, type) === null;
}

export function assertValidAnnotationValue(
  value: unknown,
  type: unknown,
  annotation = '<value>',
): string {
  const error = getAnnotationValueError(value, type);
  if (error !== null) {
    throw new AnnotationValidationError(
      `Invalid value for annotation "${annotation}" (type ${String(type)}): ${error}`,
      annotation,
      String(type),
    );
  }
  return coerceAnnotationValue(value) as string;
}

export function normalizeAllowedDirectives(
  input: unknown,
): Set<string> | null {
  if (input === undefined || input === null) return null;

  if (typeof input === 'string') {
    const names = input
      .split(/[,\s]+/)
      .map((name) => name.trim())
      .filter((name) => name !== '');
    return new Set(names);
  }

  if (Array.isArray(input)) {
    return new Set(
      input
        .filter((name): name is string => typeof name === 'string')
        .map((name) => name.trim())
        .filter((name) => name !== ''),
    );
  }

  if (input instanceof Set) {
    return new Set(
      Array.from(input)
        .filter((name): name is string => typeof name === 'string')
        .map((name) => name.trim())
        .filter((name) => name !== ''),
    );
  }

  if (isPlainObject(input)) {
    const nested =
      input.allowedDirectives ??
      input.whitelist ??
      input.allowed ??
      input.directives;
    if (nested !== undefined && nested !== input) {
      return normalizeAllowedDirectives(nested);
    }
  }

  return null;
}

function extractDirectiveName(
  statement: string,
  index: number,
  errors: string[],
): string | null {
  const trimmed = statement.trim();
  if (trimmed === '') return null;

  const match = /^([^\s{}(),;]+)/.exec(trimmed);
  if (match === null) {
    errors.push(`missing directive name at index ${index}`);
    return null;
  }

  const name = match[1];
  if (!DIRECTIVE_PATTERN.test(name)) {
    errors.push(`invalid directive name "${name}" at index ${index}`);
  }
  return name;
}

export function inspectConfig(
  config: unknown,
  allowedDirectives?: AllowedDirectives | null,
): ConfigInspection {
  const errors: string[] = [];
  const directives: string[] = [];
  let braceDepth = 0;
  let parenDepth = 0;
  let statements = 0;

  if (typeof config !== 'string') {
    return {
      ok: false,
      valid: false,
      errors: ['config must be a string'],
      directives,
      disallowedDirectives: [],
      braceDepth,
      depth: braceDepth,
      blockDepth: braceDepth,
      parenDepth,
      parenthesisDepth: parenDepth,
      statements,
    };
  }

  let buffer = '';
  let inSingleQuote = false;
  let inDoubleQuote = false;
  let escaped = false;
  let inComment = false;

  const takeDirective = (text: string, index: number): void => {
    const name = extractDirectiveName(text, index, errors);
    if (name !== null) directives.push(name);
  };

  for (let index = 0; index < config.length; index += 1) {
    const character = config[index];

    if (inComment) {
      if (character === '\n') inComment = false;
      continue;
    }

    if (inSingleQuote) {
      buffer += character;
      if (escaped) {
        escaped = false;
      } else if (character === '\\') {
        escaped = true;
      } else if (character === "'") {
        inSingleQuote = false;
      }
      continue;
    }

    if (inDoubleQuote) {
      buffer += character;
      if (escaped) {
        escaped = false;
      } else if (character === '\\') {
        escaped = true;
      } else if (character === '"') {
        inDoubleQuote = false;
      }
      continue;
    }

    if (character === '#') {
      inComment = true;
      continue;
    }

    if (character === "'") {
      inSingleQuote = true;
      buffer += character;
      continue;
    }

    if (character === '"') {
      inDoubleQuote = true;
      buffer += character;
      continue;
    }

    if (character === '{') {
      if (buffer.trim() === '') {
        errors.push(`empty block header at index ${index}`);
      } else {
        takeDirective(buffer, index);
      }
      braceDepth += 1;
      buffer = '';
      continue;
    }

    if (character === '}') {
      if (buffer.trim() !== '') {
        errors.push(`missing ";" before "}" at index ${index}`);
      }
      braceDepth -= 1;
      if (braceDepth < 0) {
        errors.push(`unexpected "}" at index ${index}`);
      }
      buffer = '';
      continue;
    }

    if (character === ';') {
      if (buffer.trim() === '') {
        errors.push(`empty statement at index ${index}`);
      } else {
        takeDirective(buffer, index);
        statements += 1;
      }
      buffer = '';
      continue;
    }

    if (character === '(') {
      parenDepth += 1;
      buffer += character;
      continue;
    }

    if (character === ')') {
      parenDepth -= 1;
      if (parenDepth < 0) {
        errors.push(`unexpected ")" at index ${index}`);
      }
      buffer += character;
      continue;
    }

    buffer += character;
  }

  if (inSingleQuote) errors.push('unterminated single-quoted string');
  if (inDoubleQuote) errors.push('unterminated double-quoted string');
  if (braceDepth !== 0) {
    errors.push(`block depth must return to 0 (ended at ${braceDepth})`);
  }
  if (parenDepth !== 0) {
    errors.push(`parenthesis depth must return to 0 (ended at ${parenDepth})`);
  }
  if (buffer.trim() !== '') {
    errors.push('unterminated statement (missing ";")');
  }

  const allowed = normalizeAllowedDirectives(allowedDirectives);
  const disallowedDirectives: string[] = [];
  if (allowed !== null) {
    for (const name of new Set(directives)) {
      if (!allowed.has(name)) {
        disallowedDirectives.push(name);
        errors.push(
          `directive "${name}" is not in the caller-provided whitelist`,
        );
      }
    }
  }

  return {
    ok: errors.length === 0,
    valid: errors.length === 0,
    errors,
    directives: Array.from(new Set(directives)),
    disallowedDirectives,
    braceDepth,
    depth: braceDepth,
    blockDepth: braceDepth,
    parenDepth,
    parenthesisDepth: parenDepth,
    statements,
  };
}

export function validateConfig(
  config: unknown,
  allowedDirectives?: AllowedDirectives | null,
): boolean {
  return inspectConfig(config, allowedDirectives).ok;
}

export function parseDirectives(config: unknown): string[] {
  return inspectConfig(config).directives;
}

export function getConfigErrors(
  config: unknown,
  allowedDirectives?: AllowedDirectives | null,
): string[] {
  return inspectConfig(config, allowedDirectives).errors;
}

export function assertValidConfig(
  config: unknown,
  allowedDirectives?: AllowedDirectives | null,
): string {
  if (typeof config !== 'string') {
    throw new TypeError('config must be a string');
  }
  const inspection = inspectConfig(config, allowedDirectives);
  if (!inspection.ok) {
    throw new ConfigValidationError(
      `Invalid rendered config: ${inspection.errors.join('; ')}`,
      inspection,
    );
  }
  return config;
}

function normalizeTemplateTuple(
  value: readonly unknown[],
): DirectiveTemplate {
  if (value.length >= 4) {
    return {
      annotation: value[0] as string,
      directive: value[1] as string,
      type: value[2] as string,
      template: value[3] as string,
    };
  }
  if (value.length === 3) {
    return {
      annotation: value[0] as string,
      type: value[1] as string,
      template: value[2] as string,
    };
  }
  throw new TemplateValidationError(
    'tuple templates must be [annotation, type, template] or [annotation, directive, type, template]',
  );
}

function normalizeOneTemplate(
  item: unknown,
  mapKey?: string,
): NormalizedDirectiveTemplate {
  let spec: DirectiveTemplate;
  if (Array.isArray(item)) {
    spec = normalizeTemplateTuple(item);
  } else if (typeof item === 'string') {
    if (mapKey !== undefined && normalizeAnnotationValueType(item) !== null) {
      spec = { annotation: mapKey, type: item };
      if (DIRECTIVE_PATTERN.test(mapKey)) spec.directive = mapKey;
    } else {
      spec = { annotation: mapKey, template: item };
    }
  } else if (isPlainObject(item)) {
    spec = item as DirectiveTemplate;
  } else {
    throw new TemplateValidationError('template entries must be objects or tuples');
  }

  const explicitAnnotation = firstString(
    spec.annotation,
    spec.annotationKey,
    spec.annotationName,
    spec.key,
  );
  const annotation = explicitAnnotation ?? mapKey ?? firstString(spec.name);
  if (annotation === undefined) {
    throw new TemplateValidationError(
      'each template must declare an annotation key',
    );
  }

  let declaredDirective = firstString(
    spec.directive,
    spec.directiveName,
    spec.target,
    spec.instruction,
  );
  const declaredType = firstString(
    spec.type,
    spec.valueType,
    spec.annotationType,
    spec.kind,
    spec.valueKind,
    spec.argType,
    spec.parameterType,
  );
  if (declaredType === undefined) {
    throw new TemplateValidationError(
      `template for annotation "${annotation}" must declare a value type`,
    );
  }
  const type = normalizeAnnotationValueType(declaredType);
  if (type === null) {
    throw new TemplateValidationError(
      `template for annotation "${annotation}" declares unsupported type "${declaredType}"`,
    );
  }

  const rawSpec = spec as unknown as Record<string, unknown>;
  const functionTemplate =
    typeof rawSpec.template === 'function'
      ? (rawSpec.template as (
          value: string,
          annotation?: string,
        ) => string)
      : typeof rawSpec.format === 'function'
        ? (rawSpec.format as (
            value: string,
            annotation?: string,
          ) => string)
        : typeof rawSpec.directiveTemplate === 'function'
          ? (rawSpec.directiveTemplate as (
              value: string,
              annotation?: string,
            ) => string)
          : undefined;
  const renderer = spec.render ?? spec.build ?? functionTemplate;
  if (renderer !== undefined && typeof renderer !== 'function') {
    throw new TemplateValidationError(
      `renderer for annotation "${annotation}" must be a function`,
    );
  }

  let template = firstString(
    spec.template,
    spec.format,
    spec.directiveTemplate,
    spec.pattern,
  );
  if (
    template === undefined &&
    declaredDirective !== undefined &&
    (declaredDirective.includes(';') ||
      declaredDirective.includes('{{') ||
      declaredDirective.includes('${') ||
      declaredDirective.includes('%s'))
  ) {
    template = declaredDirective;
    declaredDirective = undefined;
  }

  const directive =
    declaredDirective ??
    (spec.name !== annotation ? firstString(spec.name) : undefined) ??
    (mapKey !== undefined && explicitAnnotation !== undefined
      ? mapKey
      : undefined) ??
    (mapKey !== undefined && DIRECTIVE_PATTERN.test(mapKey) ? mapKey : undefined);

  if (template === undefined && renderer === undefined) {
    if (directive === undefined) {
      throw new TemplateValidationError(
        `template for annotation "${annotation}" must define a template or directive`,
      );
    }
    template = `${directive} {{value}};`;
  }

  return {
    annotation,
    directive,
    type,
    template,
    placeholder: firstString(spec.placeholder, spec.valuePlaceholder),
    render: renderer,
  };
}

export function normalizeTemplates(input: unknown): NormalizedDirectiveTemplate[] {
  let entries: Array<[unknown, string | undefined]>;

  if (Array.isArray(input)) {
    entries = input.map((item) => [item, undefined]);
  } else if (input instanceof Map) {
    entries = Array.from(input.entries()).map(([key, value]) => [
      value,
      String(key),
    ]);
  } else if (isPlainObject(input)) {
    if (Array.isArray(input.templates)) {
      entries = input.templates.map((item) => [item, undefined]);
    } else if (isPlainObject(input.templates)) {
      entries = Object.entries(input.templates).map(([key, value]) => [
        value,
        key,
      ]);
    } else if (
      (typeof input.type === 'string' ||
        typeof input.valueType === 'string' ||
        typeof input.annotationType === 'string' ||
        typeof input.kind === 'string') &&
      (typeof input.template === 'string' ||
        typeof input.format === 'string' ||
        typeof input.directive === 'string')
    ) {
      entries = [[input, undefined]];
    } else {
      entries = Object.entries(input).map(([key, value]) => [value, key]);
    }
  } else {
    throw new TemplateValidationError(
      'templates must be an array or an annotation-keyed object',
    );
  }

  return entries.map(([item, mapKey]) => normalizeOneTemplate(item, mapKey));
}

const DEFAULT_PLACEHOLDERS = [
  '{{value}}',
  '{{ value }}',
  '{{.Value}}',
  '{{ .Value }}',
  '{{.}}',
  '{{VALUE}}',
  '{{ VALUE }}',
  '{value}',
  '{VALUE}',
  '{0}',
  '${value}',
  '%VALUE%',
  '%v',
  '__VALUE__',
];

function renderTemplateValue(
  template: NormalizedDirectiveTemplate,
  value: string,
): string {
  if (template.render !== undefined) {
    const rendered = template.render(value, template.annotation);
    if (typeof rendered !== 'string') {
      throw new TemplateValidationError(
        `renderer for annotation "${template.annotation}" did not return a string`,
      );
    }
    return rendered;
  }

  const source = template.template as string;
  const placeholders =
    template.placeholder !== undefined
      ? [template.placeholder]
      : DEFAULT_PLACEHOLDERS;

  for (const placeholder of placeholders) {
    if (source.includes(placeholder)) {
      return source.split(placeholder).join(value);
    }
  }

  if (source.includes('%s')) {
    return source.split('%s').join(value);
  }

  const trimmed = source.trim();
  if (DIRECTIVE_PATTERN.test(trimmed)) {
    return `${trimmed} ${value};`;
  }

  throw new TemplateValidationError(
    `template for annotation "${template.annotation}" does not contain a value placeholder`,
  );
}

function normalizeRenderOptions(input: unknown): RenderOptions {
  if (input === undefined || input === null) return {};
  if (
    Array.isArray(input) ||
    typeof input === 'string' ||
    input instanceof Set
  ) {
    return { allowedDirectives: input as AllowedDirectives };
  }
  if (isPlainObject(input)) return { ...input } as RenderOptions;
  return {};
}

function isDirectiveTemplateLike(value: unknown): value is DirectiveTemplate {
  if (!isPlainObject(value)) return false;
  const hasType =
    typeof value.type === 'string' ||
    typeof value.valueType === 'string' ||
    typeof value.annotationType === 'string' ||
    typeof value.kind === 'string';
  const hasTemplate =
    typeof value.template === 'string' ||
    typeof value.format === 'string' ||
    typeof value.directive === 'string' ||
    typeof value.render === 'function' ||
    typeof value.build === 'function';
  return hasType && hasTemplate;
}

function isWhitelistInput(value: unknown): boolean {
  if (typeof value === 'string') return true;
  if (value instanceof Set) return true;
  if (Array.isArray(value)) {
    return value.every((item) => typeof item === 'string');
  }
  if (isPlainObject(value)) {
    return (
      value.allowedDirectives !== undefined ||
      value.whitelist !== undefined ||
      value.allowed !== undefined ||
      value.directives !== undefined
    ) && value.templates === undefined && value.type === undefined;
  }
  return false;
}

function isTemplatesInput(value: unknown): boolean {
  if (value instanceof Map) return true;
  if (Array.isArray(value)) {
    return value.some(
      (item) => Array.isArray(item) || isPlainObject(item),
    );
  }
  if (isPlainObject(value)) {
    if (value.templates !== undefined) return true;
    if (typeof value.type === 'string' && isDirectiveTemplateLike(value)) {
      return true;
    }
    const values = Object.values(value);
    return (
      values.length > 0 &&
      values.every((item) => isPlainObject(item) || Array.isArray(item))
    );
  }
  return false;
}

function resolveRenderArguments(
  templates: unknown,
  options: unknown,
): { templates: unknown; options: unknown } {
  if (isWhitelistInput(templates) && isTemplatesInput(options)) {
    return { templates: options, options: templates };
  }
  return { templates, options };
}

function initializeReport(report?: RenderReport): RenderReport {
  if (report === undefined) {
    return {
      rendered: [],
      dropped: [],
      directives: [],
      unknownKeys: [],
      warnings: [],
    };
  }
  if (!Array.isArray(report.rendered)) report.rendered = [];
  if (!Array.isArray(report.dropped)) report.dropped = [];
  if (!Array.isArray(report.directives)) report.directives = [];
  if (!Array.isArray(report.unknownKeys)) report.unknownKeys = [];
  if (!Array.isArray(report.warnings)) report.warnings = [];
  return report;
}

function emitLog(options: RenderOptions, event: RenderLogEvent): void {
  if (options.logger === false || options.logger === null) return;
  if (typeof options.logger === 'function') {
    options.logger(event.message, event);
    return;
  }
  if (typeof console !== 'undefined' && typeof console.warn === 'function') {
    console.warn(event.message);
  }
}

function recordDrop(
  options: RenderOptions,
  report: RenderReport,
  drop: DroppedAnnotation,
  type:
    | 'unknown-annotation'
    | 'invalid-annotation'
    | 'duplicate-template'
    | 'dropped-annotation',
): void {
  report.dropped.push(drop);
  report.warnings?.push(drop.message);
  if (Array.isArray(options.dropped)) options.dropped.push(drop);
  if (Array.isArray(options.warnings)) options.warnings.push(drop.message);
  if (Array.isArray(options.log)) options.log.push(drop.message);
  if (
    drop.reason === 'unknown-annotation' &&
    Array.isArray(options.unknownKeys)
  ) {
    options.unknownKeys.push(drop.key);
  }

  const event: RenderLogEvent = {
    type,
    level: type === 'invalid-annotation' ? 'error' : 'warn',
    key: drop.key,
    value: drop.value,
    message: drop.message,
  };

  emitLog(options, event);
  if (typeof options.onDrop === 'function') options.onDrop(drop, event);
}

function resolveWhitelist(options: RenderOptions): Set<string> | null {
  return normalizeAllowedDirectives(
    options.allowedDirectives ??
      options.whitelist ??
      options.allowed ??
      options.directiveWhitelist,
  );
}

function renderAnnotationsInternal(
  annotations: Record<string, unknown>,
  templatesInput: unknown,
  optionsInput?: RenderOptions | AllowedDirectives,
): RenderResult {
  const options = normalizeRenderOptions(optionsInput);
  if (
    options.allowedDirectives === undefined &&
    options.whitelist === undefined &&
    options.allowed === undefined &&
    options.directiveWhitelist === undefined &&
    isPlainObject(templatesInput) &&
    templatesInput.templates !== undefined
  ) {
    const nestedWhitelist =
      templatesInput.allowedDirectives ??
      templatesInput.whitelist ??
      templatesInput.allowed ??
      templatesInput.directives;
    if (
      nestedWhitelist !== undefined &&
      (typeof nestedWhitelist === 'string' ||
        Array.isArray(nestedWhitelist) ||
        nestedWhitelist instanceof Set)
    ) {
      options.allowedDirectives = nestedWhitelist as
        | readonly string[]
        | Set<string>
        | string;
    }
  }
  const templates = normalizeTemplates(templatesInput);
  const report = initializeReport(options.report);

  if (!isPlainObject(annotations)) {
    throw new TypeError('annotations must be an object of key/value pairs');
  }

  const orderedTemplates: NormalizedDirectiveTemplate[] = [];
  const byAnnotation = new Map<string, NormalizedDirectiveTemplate>();
  for (const template of templates) {
    if (byAnnotation.has(template.annotation)) {
      const message = `Duplicate template for annotation "${template.annotation}" was ignored`;
      emitLog(options, {
        type: 'duplicate-template',
        level: 'warn',
        key: template.annotation,
        message,
      });
      continue;
    }
    byAnnotation.set(template.annotation, template);
    orderedTemplates.push(template);
  }

  const invalidPolicy =
    options.invalidValuePolicy ?? (options.strict === false ? 'drop' : 'throw');
  const unknownPolicy = options.unknownAnnotationPolicy ?? 'drop';

  const renderedParts: string[] = [];
  const knownAnnotationKeys = new Set<string>();

  for (const template of orderedTemplates) {
    const key = template.annotation;
    if (!Object.prototype.hasOwnProperty.call(annotations, key)) continue;
    knownAnnotationKeys.add(key);

    const rawValue = annotations[key];
    const valueError = getAnnotationValueError(rawValue, template.type);
    if (valueError !== null) {
      const message = `Invalid value for annotation "${key}" (type ${template.type}): ${valueError}`;
      const drop: DroppedAnnotation = {
        key,
        value: rawValue,
        reason: 'invalid-value',
        message,
      };

      if (invalidPolicy === 'drop') {
        recordDrop(options, report, drop, 'dropped-annotation');
        if (typeof options.onInvalidAnnotation === 'function') {
          options.onInvalidAnnotation(key, message);
        } else if (typeof options.onInvalid === 'function') {
          options.onInvalid(key, message);
        }
        continue;
      }

      throw new AnnotationValidationError(message, key, template.type);
    }

    const value = coerceAnnotationValue(rawValue) as string;
    let snippet: string;
    try {
      snippet = renderTemplateValue(template, value);
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);
      throw new TemplateValidationError(
        `Failed to render annotation "${key}": ${detail}`,
      );
    }

    const trimmedSnippet = snippet.trim();
    if (trimmedSnippet !== '') {
      renderedParts.push(trimmedSnippet);
      report.rendered.push(trimmedSnippet);
    }
  }

  for (const key of Object.keys(annotations)) {
    if (knownAnnotationKeys.has(key)) continue;

    const message = `Unknown annotation "${key}" was dropped`;
    report.unknownKeys.push(key);
    const drop: DroppedAnnotation = {
      key,
      value: annotations[key],
      reason: 'unknown-annotation',
      message,
    };

    recordDrop(options, report, drop, 'unknown-annotation');
    if (typeof options.onUnknownAnnotation === 'function') {
      options.onUnknownAnnotation(key, {
        type: 'unknown-annotation',
        level: 'warn',
        key,
        value: annotations[key],
        message,
      });
    } else if (typeof options.onUnknown === 'function') {
      options.onUnknown(key, {
        type: 'unknown-annotation',
        level: 'warn',
        key,
        value: annotations[key],
        message,
      });
    }

    if (unknownPolicy === 'throw') {
      throw new UnknownAnnotationError(message, key);
    }
  }

  const separator = options.separator ?? '\n';
  const body = renderedParts.join(separator);
  const baseConfig =
    firstString(
      options.baseConfig,
      options.base,
      options.header,
      options.prefix,
    ) ?? '';

  let config = body;
  if (baseConfig !== '') {
    config =
      baseConfig +
      (body !== '' && !baseConfig.endsWith('\n') ? '\n' : '') +
      body;
  }
  if (
    options.trailingNewline === true &&
    config !== '' &&
    !config.endsWith('\n')
  ) {
    config += '\n';
  }

  let allowed = resolveWhitelist(options);
  if (allowed !== null && baseConfig !== '') {
    const baseInspection = inspectConfig(baseConfig);
    const withBase = new Set(allowed);
    for (const directive of baseInspection.directives) withBase.add(directive);
    allowed = withBase;
  }

  const shouldValidate =
    options.validateConfig !== false && options.skipConfigValidation !== true;
  const inspection = inspectConfig(config, allowed);
  report.directives = inspection.directives;

  if (shouldValidate && !inspection.ok) {
    throw new ConfigValidationError(
      `Invalid rendered config: ${inspection.errors.join('; ')}`,
      inspection,
    );
  }

  const droppedKeys = Array.from(
    new Set(report.dropped.map((drop) => drop.key)),
  );

  return {
    config,
    report,
    dropped: droppedKeys,
    droppedDetails: report.dropped,
    droppedKeys,
    directives: inspection.directives,
    warnings: report.warnings ?? [],
    unknownKeys: report.unknownKeys,
    rendered: report.rendered,
    errors: inspection.errors,
    inspection,
  };
}

export function renderAnnotations(
  annotations: Record<string, unknown>,
  templates: DirectiveTemplates,
  options?: RenderOptions | AllowedDirectives,
): string {
  const resolved = resolveRenderArguments(templates, options);
  return renderAnnotationsInternal(
    annotations,
    resolved.templates,
    resolved.options as RenderOptions | AllowedDirectives | undefined,
  ).config;
}

export function renderAnnotationsDetailed(
  annotations: Record<string, unknown>,
  templates: DirectiveTemplates,
  options?: RenderOptions | AllowedDirectives,
): RenderResult {
  const resolved = resolveRenderArguments(templates, options);
  return renderAnnotationsInternal(
    annotations,
    resolved.templates,
    resolved.options as RenderOptions | AllowedDirectives | undefined,
  );
}

export function buildAnnotationConfig(
  annotations: Record<string, unknown>,
  templates: DirectiveTemplates,
  options?: RenderOptions | AllowedDirectives,
): RenderResult {
  const resolved = resolveRenderArguments(templates, options);
  return renderAnnotationsInternal(
    annotations,
    resolved.templates,
    resolved.options as RenderOptions | AllowedDirectives | undefined,
  );
}

export function renderAnnotation(
  annotationsOrKey: Record<string, unknown> | string,
  templatesOrValue: unknown,
  optionsOrTemplate?: unknown,
  maybeOptions?: RenderOptions | AllowedDirectives,
): string {
  if (
    typeof annotationsOrKey === 'string' &&
    (typeof templatesOrValue === 'string' ||
      typeof templatesOrValue === 'number') &&
    normalizeAnnotationValueType(optionsOrTemplate) !== null
  ) {
    const spec: DirectiveTemplate = {
      annotation: '__value__',
      type: optionsOrTemplate as string,
      template: annotationsOrKey,
    };
    return renderAnnotations(
      { __value__: templatesOrValue },
      [spec],
      maybeOptions,
    );
  }

  if (isDirectiveTemplateLike(annotationsOrKey)) {
    const spec = { ...annotationsOrKey };
    let options = maybeOptions;
    if (normalizeAnnotationValueType(optionsOrTemplate) !== null) {
      if (
        typeof spec.type !== 'string' &&
        typeof spec.valueType !== 'string' &&
        typeof spec.annotationType !== 'string' &&
        typeof spec.kind !== 'string'
      ) {
        spec.type = optionsOrTemplate as string;
      }
    } else if (optionsOrTemplate !== undefined) {
      options = optionsOrTemplate as RenderOptions | AllowedDirectives;
    }
    const key = firstString(spec.annotation, spec.annotationKey, spec.key) ??
      '__value__';
    return renderAnnotations(
      { [key]: templatesOrValue },
      [spec],
      options,
    );
  }

  if (typeof annotationsOrKey === 'string') {
    const annotations: Record<string, unknown> = {
      [annotationsOrKey]: templatesOrValue,
    };
    return renderAnnotations(
      annotations,
      optionsOrTemplate as DirectiveTemplates,
      maybeOptions,
    );
  }
  return renderAnnotations(
    annotationsOrKey,
    templatesOrValue as DirectiveTemplates,
    optionsOrTemplate as RenderOptions | AllowedDirectives | undefined,
  );
}

export class AnnotationRenderer {
  templates: DirectiveTemplates;
  options: RenderOptions;

  constructor(templates: DirectiveTemplates, options: RenderOptions = {}) {
    this.templates = templates;
    this.options = options;
  }

  render(annotations: Record<string, unknown>): string {
    return renderAnnotations(annotations, this.templates, this.options);
  }

  renderDetailed(annotations: Record<string, unknown>): RenderResult {
    return renderAnnotationsInternal(
      annotations,
      this.templates,
      this.options,
    );
  }

  validate(value: unknown, type: unknown): boolean {
    return validateAnnotationValue(value, type);
  }

  validateConfig(config: unknown, allowed?: AllowedDirectives): boolean {
    return validateConfig(
      config,
      allowed ?? this.options.allowedDirectives ?? this.options.whitelist,
    );
  }
}

export const render = renderAnnotations;
export const renderConfig = renderAnnotations;
export const renderAnnotationConfig = renderAnnotations;
export const renderIngressAnnotations = renderAnnotations;
export const renderNginxAnnotations = renderAnnotations;
export const renderAnnotationSet = renderAnnotations;
export const buildConfig = renderAnnotations;
export const renderAnnotationsWithReport = renderAnnotationsDetailed;
export const validateValue = validateAnnotationValue;
export const validateAnnotation = validateAnnotationValue;
export const isValidAnnotationValue = validateAnnotationValue;
export const isValidValue = validateAnnotationValue;
export const assertAnnotationValue = assertValidAnnotationValue;
export const validateRenderedConfig = validateConfig;
export const validateAnnotationConfig = validateConfig;
export const parseConfig = inspectConfig;
export const checkConfig = inspectConfig;
export const validateConfigDetailed = inspectConfig;

const annotationRendererApi = Object.assign(renderAnnotations, {
  render: renderAnnotations,
  renderConfig: renderAnnotations,
  renderAnnotations,
  renderAnnotationsDetailed,
  renderAnnotationsWithReport: renderAnnotationsDetailed,
  renderAnnotationConfig,
  renderIngressAnnotations,
  renderNginxAnnotations,
  renderAnnotation,
  buildAnnotationConfig,
  buildConfig,
  validateAnnotationValue,
  validateValue,
  validateAnnotation,
  isValidAnnotationValue,
  isValidValue,
  assertValidAnnotationValue,
  assertAnnotationValue,
  getAnnotationValueError,
  normalizeAnnotationValueType,
  normalizeAllowedDirectives,
  normalizeTemplates,
  validateConfig,
  validateRenderedConfig,
  validateAnnotationConfig,
  validateConfigDetailed: inspectConfig,
  inspectConfig,
  checkConfig: inspectConfig,
  parseConfig,
  parseDirectives,
  getConfigErrors,
  assertValidConfig,
  AnnotationRenderer,
  AnnotationValidationError,
  TemplateValidationError,
  UnknownAnnotationError,
  ConfigValidationError,
});

export default annotationRendererApi;
