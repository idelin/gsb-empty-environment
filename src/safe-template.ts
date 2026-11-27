/**
 * Zero-dependency safe template lexer, parser and AST-walking sandbox.
 *
 * The implementation deliberately contains no eval/Function/vm integration.
 * Every dynamic value is interpreted from a closed AST.
 */

export type FilterFunction = (value: unknown, ...args: unknown[]) => unknown;
export type FilterMap = Record<string, FilterFunction>;
export type EscapeFunction = (value: string) => string;

export interface QuotaOptions {
  maxAstDepth?: number;
  maxDepth?: number;
  astDepthLimit?: number;
  maxLoopIterations?: number;
  maxIterations?: number;
  loopIterationLimit?: number;
  maxOutputBytes?: number;
  maxOutputSize?: number;
  outputByteLimit?: number;
  maxEvaluationSteps?: number;
  maxSteps?: number;
  evaluationStepLimit?: number;
  maxTemplateLength?: number;
  maxExpressionLength?: number;
  maxParserNesting?: number;
}

export interface SafeTemplateOptions extends QuotaOptions {
  filters?: FilterMap;
  quotas?: QuotaOptions;
  limits?: QuotaOptions;
  escape?: boolean | 'html' | EscapeFunction;
  strictVariables?: boolean;
}

export type TemplateOptionInput =
  | SafeTemplateOptions
  | QuotaOptions
  | FilterMap
  | NormalizedOptions;

export interface NormalizedQuota {
  maxAstDepth: number;
  maxLoopIterations: number;
  maxOutputBytes: number;
  maxEvaluationSteps: number;
  maxTemplateLength: number;
  maxExpressionLength: number;
  maxParserNesting: number;
}

export const DEFAULT_QUOTA: NormalizedQuota = Object.freeze({
  maxAstDepth: 96,
  maxLoopIterations: 10_000,
  maxOutputBytes: 262_144,
  maxEvaluationSteps: 250_000,
  maxTemplateLength: 1_000_000,
  maxExpressionLength: 10_000,
  maxParserNesting: 256,
});

export class SafeTemplateError extends Error {
  code: string;
  details?: Record<string, unknown>;

  constructor(message: string, code = 'SAFE_TEMPLATE_ERROR', details?: Record<string, unknown>) {
    super(message);
    this.name = 'SafeTemplateError';
    this.code = code;
    if (details !== undefined) this.details = details;
  }
}

export class TemplateParseError extends SafeTemplateError {
  index?: number;
  line?: number;
  column?: number;

  constructor(message: string, index?: number, source?: string) {
    super(message, 'TEMPLATE_PARSE_ERROR', index === undefined ? undefined : { index });
    this.name = 'TemplateParseError';
    if (index !== undefined) {
      this.index = index;
      if (source !== undefined) {
        const location = locatePosition(source, index);
        this.line = location.line;
        this.column = location.column;
        this.details = { index, line: location.line, column: location.column };
      }
    }
  }
}

export class TemplateSecurityError extends SafeTemplateError {
  constructor(message: string, details?: Record<string, unknown>) {
    super(message, 'TEMPLATE_SECURITY_ERROR', details);
    this.name = 'TemplateSecurityError';
  }
}

export class TemplateQuotaError extends SafeTemplateError {
  quota: string;
  limit: number;
  actual?: number;

  constructor(quota: string, message: string, limit: number, actual?: number) {
    super(message, 'TEMPLATE_QUOTA_EXCEEDED', { quota, limit, actual });
    this.name = 'TemplateQuotaError';
    this.quota = quota;
    this.limit = limit;
    this.actual = actual;
  }
}

export class TemplateRuntimeError extends SafeTemplateError {
  constructor(message: string, details?: Record<string, unknown>) {
    super(message, 'TEMPLATE_RUNTIME_ERROR', details);
    this.name = 'TemplateRuntimeError';
  }
}

export const TemplateError = SafeTemplateError;
export const ParseError = TemplateParseError;
export const SecurityError = TemplateSecurityError;
export const QuotaError = TemplateQuotaError;
export const QuotaExceededError = TemplateQuotaError;
export const RuntimeTemplateError = TemplateRuntimeError;

export function locatePosition(source: string, index: number): { line: number; column: number } {
  let line = 1;
  let lineStart = 0;
  const end = Math.max(0, Math.min(index, source.length));
  for (let i = 0; i < end; i += 1) {
    if (source[i] === '\n') {
      line += 1;
      lineStart = i + 1;
    }
  }
  return { line, column: end - lineStart + 1 };
}

function toFiniteQuotaInteger(value: unknown, fallback: number, minimum: number): number {
  if (value === undefined) return fallback;
  if (typeof value !== 'number' || !Number.isFinite(value) || !Number.isInteger(value)) {
    throw new TypeError('Template quota values must be finite integers');
  }
  if (value < minimum) {
    throw new TypeError(`Template quota values must be >= ${minimum}`);
  }
  return value;
}

export function normalizeQuota(input: QuotaOptions = {}): NormalizedQuota {
  const pick = <T,>(...values: Array<T | undefined>): T | undefined => {
    for (const value of values) if (value !== undefined) return value;
    return undefined;
  };

  return {
    maxAstDepth: toFiniteQuotaInteger(
      pick(input.maxAstDepth, input.maxDepth, input.astDepthLimit),
      DEFAULT_QUOTA.maxAstDepth,
      1,
    ),
    maxLoopIterations: toFiniteQuotaInteger(
      pick(input.maxLoopIterations, input.maxIterations, input.loopIterationLimit),
      DEFAULT_QUOTA.maxLoopIterations,
      0,
    ),
    maxOutputBytes: toFiniteQuotaInteger(
      pick(input.maxOutputBytes, input.maxOutputSize, input.outputByteLimit),
      DEFAULT_QUOTA.maxOutputBytes,
      0,
    ),
    maxEvaluationSteps: toFiniteQuotaInteger(
      pick(input.maxEvaluationSteps, input.maxSteps, input.evaluationStepLimit),
      DEFAULT_QUOTA.maxEvaluationSteps,
      0,
    ),
    maxTemplateLength: toFiniteQuotaInteger(
      input.maxTemplateLength,
      DEFAULT_QUOTA.maxTemplateLength,
      1,
    ),
    maxExpressionLength: toFiniteQuotaInteger(
      input.maxExpressionLength,
      DEFAULT_QUOTA.maxExpressionLength,
      1,
    ),
    maxParserNesting: toFiniteQuotaInteger(
      input.maxParserNesting,
      DEFAULT_QUOTA.maxParserNesting,
      1,
    ),
  };
}

export interface NormalizedOptions {
  filters: FilterMap;
  quota: NormalizedQuota;
  escape: false | EscapeFunction;
  strictVariables: boolean;
}

function isSafeTemplateOptions(value: unknown): value is SafeTemplateOptions {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Record<string, unknown>;
  return (
    'filters' in candidate ||
    'quotas' in candidate ||
    'limits' in candidate ||
    'escape' in candidate ||
    'strictVariables' in candidate
  );
}

function isBareFilterMap(value: unknown): value is FilterMap {
  if (!value || typeof value !== 'object' || isSafeTemplateOptions(value)) return false;
  return Object.values(value as Record<string, unknown>).some((item) => typeof item === 'function');
}

function normalizeOptions(
  input?: TemplateOptionInput,
): NormalizedOptions {
  if (
    input &&
    typeof input === 'object' &&
    'quota' in input &&
    'filters' in input &&
    'escape' in input
  ) {
    return input as NormalizedOptions;
  }

  let options: SafeTemplateOptions = {};
  let quotaInput: QuotaOptions = {};

  if (input !== undefined) {
    if (isSafeTemplateOptions(input)) {
      options = input;
      quotaInput = {
        ...input,
        ...(input.quotas ?? {}),
        ...(input.limits ?? {}),
      };
    } else if (isBareFilterMap(input)) {
      options = { filters: input };
    } else {
      quotaInput = input;
    }
  }

  let escape: false | EscapeFunction = false;
  if (options.escape === true || options.escape === 'html') escape = escapeHtml;
  else if (typeof options.escape === 'function') escape = options.escape as EscapeFunction;
  else if (options.escape !== undefined && options.escape !== false) {
    throw new TypeError('escape must be false, "html", true, or a function');
  }

  const filters = options.filters ?? {};
  if (!filters || typeof filters !== 'object') throw new TypeError('filters must be an object');

  return {
    filters: filters as FilterMap,
    quota: normalizeQuota(quotaInput),
    escape,
    strictVariables: options.strictVariables === true,
  };
}

/* ---------------------------------- AST ---------------------------------- */

export interface AstNode {
  kind: string;
  depth: number;
  start?: number;
  end?: number;
}

export interface TextNode extends AstNode {
  kind: 'Text';
  value: string;
}

export interface OutputNode extends AstNode {
  kind: 'Output';
  expression: Expression;
  raw: boolean;
}

export interface IfBranch {
  test: Expression | null;
  nodes: TemplateNode[];
}

export interface IfNode extends AstNode {
  kind: 'If';
  branches: IfBranch[];
}

export interface ForNode extends AstNode {
  kind: 'For';
  valueBinding: string;
  keyBinding: string | null;
  collection: Expression;
  nodes: TemplateNode[];
}

export type TemplateNode = TextNode | OutputNode | IfNode | ForNode;

export type LiteralValue = string | number | boolean | null | undefined;

export interface LiteralExpression extends AstNode {
  kind: 'Literal';
  value: LiteralValue;
}

export interface IdentifierExpression extends AstNode {
  kind: 'Identifier';
  name: string;
}

export interface MemberExpression extends AstNode {
  kind: 'Member';
  object: Expression;
  key: Expression;
  computed: boolean;
}

export interface CallExpression extends AstNode {
  kind: 'Call';
  callee: Expression;
  args: Expression[];
}

export interface FilterExpression extends AstNode {
  kind: 'Filter';
  input: Expression;
  name: string;
  args: Expression[];
}

export interface ArrayExpression extends AstNode {
  kind: 'Array';
  elements: Expression[];
}

export interface ObjectPropertyNode extends AstNode {
  kind: 'ObjectProperty';
  key: string;
  value: Expression;
}

export interface ObjectExpression extends AstNode {
  kind: 'Object';
  properties: ObjectPropertyNode[];
}

export interface UnaryExpression extends AstNode {
  kind: 'Unary';
  operator: '!' | '-' | '+';
  argument: Expression;
}

export interface BinaryExpression extends AstNode {
  kind: 'Binary';
  operator: string;
  left: Expression;
  right: Expression;
}

export interface LogicalExpression extends AstNode {
  kind: 'Logical';
  operator: '&&' | '||' | '??';
  left: Expression;
  right: Expression;
}

export interface ConditionalExpression extends AstNode {
  kind: 'Conditional';
  test: Expression;
  consequent: Expression;
  alternate: Expression;
}

export type Expression =
  | LiteralExpression
  | IdentifierExpression
  | MemberExpression
  | CallExpression
  | FilterExpression
  | ArrayExpression
  | ObjectExpression
  | UnaryExpression
  | BinaryExpression
  | LogicalExpression
  | ConditionalExpression;

/* ------------------------------ HTML helpers ----------------------------- */

export function escapeHtml(value: unknown): string {
  const text = value === null || value === undefined ? '' : primitiveOrJsonString(value);
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

export function utf8ByteLength(value: string): number {
  let bytes = 0;
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i);
    if (code < 0x80) bytes += 1;
    else if (code < 0x800) bytes += 2;
    else if (code >= 0xd800 && code <= 0xdbff) {
      bytes += 4;
      i += 1;
    } else bytes += 3;
  }
  return bytes;
}

function quoteJsonString(value: string): string {
  return JSON.stringify(value);
}

function primitiveOrJsonString(value: unknown): string {
  if (value === null) return 'null';
  if (value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') {
    return String(value);
  }
  if (typeof value === 'function') {
    throw new TemplateSecurityError('Function values cannot be rendered');
  }
  return safeJsonStringify(value);
}

export function safeJsonStringify(value: unknown, maxDepth = 32): string {
  const seen = new WeakSet<object>();
  const render = (current: unknown, depth: number): string => {
    if (current === null) return 'null';
    if (current === undefined) return 'null';
    if (typeof current === 'string') return quoteJsonString(current);
    if (typeof current === 'number' || typeof current === 'boolean') {
      return Number.isNaN(current) ? 'null' : JSON.stringify(current);
    }
    if (typeof current === 'bigint') return quoteJsonString(String(current));
    if (typeof current === 'function' || typeof current === 'symbol') {
      throw new TemplateSecurityError('Function or symbol values cannot be serialized');
    }
    if (depth >= maxDepth) throw new TemplateQuotaError('jsonDepth', 'JSON depth limit exceeded', maxDepth);
    if (seen.has(current as object)) throw new TemplateRuntimeError('Circular value cannot be serialized');
    seen.add(current as object);
    try {
      if (Array.isArray(current)) {
        const items: string[] = [];
        for (let i = 0; i < current.length; i += 1) {
          if (Object.hasOwn(current, i)) items.push(render(current[i], depth + 1));
          else items.push('null');
        }
        return `[${items.join(',')}]`;
      }
      if (typeof current === 'object') {
        const record = current as Record<string, unknown>;
        const parts: string[] = [];
        for (const key of Object.keys(record)) {
          if (!Object.hasOwn(record, key)) continue;
          parts.push(`${quoteJsonString(key)}:${render(record[key], depth + 1)}`);
        }
        return `{${parts.join(',')}}`;
      }
      return 'null';
    } finally {
      seen.delete(current as object);
    }
  };
  return render(value, 0);
}

/* ---------------------------- Template lexer ----------------------------- */

export type TemplateTokenKind = 'text' | 'output' | 'block' | 'comment' | 'eof';

export interface TemplateToken {
  kind: TemplateTokenKind;
  start: number;
  end: number;
  value: string;
  name?: string;
  argument?: string;
  trimBefore?: boolean;
  trimAfter?: boolean;
}

export function findClosingDelimiter(source: string, start: number, closing: string): number {
  let quote: '"' | "'" | '`' | null = null;
  let curlyDepth = 0;

  for (let i = start; i < source.length; i += 1) {
    const char = source[i];

    if (quote) {
      if (char === '\\') {
        i += 1;
        continue;
      }
      if (char === quote) quote = null;
      continue;
    }

    if (char === '"' || char === "'" || char === '`') {
      quote = char as '"' | "'" | '`';
      continue;
    }

    if (closing === '}}') {
      if (char === '{') curlyDepth += 1;
      if (char === '}') {
        if (source.startsWith('}}', i) && curlyDepth === 0) return i;
        if (curlyDepth > 0) curlyDepth -= 1;
      }
    } else if (source.startsWith(closing, i)) {
      return i;
    }
  }
  return -1;
}

function isWhitespace(char: string | undefined): boolean {
  return char !== undefined && /\s/u.test(char);
}

export function tokenizeTemplate(
  source: string,
  options?: SafeTemplateOptions | QuotaOptions,
): TemplateToken[] {
  if (typeof source !== 'string') throw new TypeError('Template source must be a string');
  const { quota } = normalizeOptions(options);
  if (source.length > quota.maxTemplateLength) {
    throw new TemplateQuotaError(
      'maxTemplateLength',
      `Template is ${source.length} characters; limit is ${quota.maxTemplateLength}`,
      quota.maxTemplateLength,
      source.length,
    );
  }

  const tokens: TemplateToken[] = [];
  let index = 0;

  const trimPreviousText = (): void => {
    const last = tokens[tokens.length - 1];
    if (last && last.kind === 'text') last.value = last.value.trimEnd();
  };

  const skipWhitespaceAfter = (position: number): number => {
    let next = position;
    while (next < source.length && isWhitespace(source[next])) next += 1;
    return next;
  };

  while (index < source.length) {
    const outputAt = source.indexOf('{{', index);
    const blockAt = source.indexOf('{%', index);
    const commentAt = source.indexOf('{#', index);
    const candidates = [outputAt, blockAt, commentAt].filter((position) => position !== -1);
    const next = candidates.length ? Math.min(...candidates) : -1;

    if (next === -1) {
      tokens.push({ kind: 'text', start: index, end: source.length, value: source.slice(index) });
      break;
    }

    if (next > index) {
      tokens.push({ kind: 'text', start: index, end: next, value: source.slice(index, next) });
    }
    index = next;

    if (source.startsWith('{#', index)) {
      let contentStart = index + 2;
      const trimBefore = source[contentStart] === '-';
      if (trimBefore) contentStart += 1;
      const close = source.indexOf('#}', contentStart);
      if (close === -1) throw new TemplateParseError('Unclosed template comment', index, source);
      let contentEnd = close;
      const trimAfter = source[contentEnd - 1] === '-';
      if (trimAfter) contentEnd -= 1;
      if (trimBefore) trimPreviousText();
      tokens.push({
        kind: 'comment',
        start: index,
        end: close + 2,
        value: source.slice(contentStart, contentEnd).trim(),
        trimBefore,
        trimAfter,
      });
      index = close + 2;
      if (trimAfter) index = skipWhitespaceAfter(index);
      continue;
    }

    const isOutput = source.startsWith('{{', index);
    const opening = isOutput ? '{{' : '{%';
    const closing = isOutput ? '}}' : '%}';
    let contentStart = index + opening.length;
    const trimBefore = source[contentStart] === '-';
    if (trimBefore) contentStart += 1;

    const close = findClosingDelimiter(source, contentStart, closing);
    if (close === -1) {
      throw new TemplateParseError(
        `Unclosed ${isOutput ? 'output tag' : 'block tag'}`,
        index,
        source,
      );
    }

    let contentEnd = close;
    const trimAfter = source[contentEnd - 1] === '-';
    if (trimAfter) contentEnd -= 1;
    const content = source.slice(contentStart, contentEnd).trim();
    const end = close + closing.length;

    if (trimBefore) trimPreviousText();

    if (isOutput) {
      tokens.push({ kind: 'output', start: index, end, value: content, trimBefore, trimAfter });
    } else {
      const match = /^([A-Za-z_][A-Za-z0-9_]*)\s*([\s\S]*)$/u.exec(content);
      if (!match) throw new TemplateParseError(`Invalid block tag: ${content}`, index, source);
      tokens.push({
        kind: 'block',
        start: index,
        end,
        value: content,
        name: match[1],
        argument: match[2].trim(),
        trimBefore,
        trimAfter,
      });
    }

    index = end;
    if (trimAfter) index = skipWhitespaceAfter(index);
  }

  tokens.push({ kind: 'eof', start: source.length, end: source.length, value: '' });
  return tokens;
}

/* --------------------------- Expression lexer --------------------------- */

type ExpressionTokenKind = 'number' | 'string' | 'ident' | 'op' | 'eof';

export interface ExpressionToken {
  kind: ExpressionTokenKind;
  value: string | number;
  index: number;
}

const OPERATORS = [
  '===',
  '!==',
  '...',
  '==',
  '!=',
  '<=',
  '>=',
  '&&',
  '||',
  '??',
  '?.',
  '=>',
  '**',
  '+',
  '-',
  '*',
  '/',
  '%',
  '<',
  '>',
  '!',
  '?',
  ':',
  '.',
  ',',
  '(',
  ')',
  '[',
  ']',
  '{',
  '}',
  '|',
  '=',
];

function readStringLiteral(input: string, start: number, quote: string): { value: string; end: number } {
  let index = start + 1;
  let value = '';

  while (index < input.length && input[index] !== quote) {
    const char = input[index];
    if (char !== '\\') {
      value += char;
      index += 1;
      continue;
    }

    index += 1;
    const escaped = input[index];
    if (escaped === undefined) throw new TemplateParseError('Unterminated string escape', index);

    if (escaped === 'u' && input[index + 1] === '{') {
      const close = input.indexOf('}', index + 2);
      if (close === -1) throw new TemplateParseError('Invalid unicode escape', index);
      const hex = input.slice(index + 2, close);
      if (!/^[0-9a-fA-F]+$/u.test(hex)) throw new TemplateParseError('Invalid unicode escape', index);
      value += String.fromCodePoint(Number.parseInt(hex, 16));
      index = close + 1;
      continue;
    }

    if (escaped === 'u') {
      const hex = input.slice(index + 1, index + 5);
      if (!/^[0-9a-fA-F]{4}$/u.test(hex)) throw new TemplateParseError('Invalid unicode escape', index);
      value += String.fromCharCode(Number.parseInt(hex, 16));
      index += 5;
      continue;
    }

    const simpleEscapes: Record<string, string> = {
      n: '\n',
      r: '\r',
      t: '\t',
      b: '\b',
      f: '\f',
      v: '\v',
      '0': '\0',
      '\\': '\\',
      "'": "'",
      '"': '"',
      '`': '`',
    };
    value += simpleEscapes[escaped] ?? escaped;
    index += 1;
  }

  if (input[index] !== quote) throw new TemplateParseError('Unterminated string literal', start);
  return { value, end: index + 1 };
}

export function tokenizeExpression(
  input: string,
  options?: SafeTemplateOptions | QuotaOptions | number,
): ExpressionToken[] {
  if (typeof input !== 'string') throw new TypeError('Expression must be a string');
  let maxLength = DEFAULT_QUOTA.maxExpressionLength;
  if (typeof options === 'number') maxLength = options;
  else if (options !== undefined) maxLength = normalizeOptions(options).quota.maxExpressionLength;

  if (input.length > maxLength) {
    throw new TemplateQuotaError(
      'maxExpressionLength',
      `Expression is ${input.length} characters; limit is ${maxLength}`,
      maxLength,
      input.length,
    );
  }

  const tokens: ExpressionToken[] = [];
  let index = 0;

  while (index < input.length) {
    const char = input[index];
    if (/\s/u.test(char)) {
      index += 1;
      continue;
    }

    if (char === '"' || char === "'" || char === '`') {
      const result = readStringLiteral(input, index, char);
      tokens.push({ kind: 'string', value: result.value, index });
      index = result.end;
      continue;
    }

    if (/[0-9]/u.test(char) || (char === '.' && /[0-9]/u.test(input[index + 1] ?? ''))) {
      const start = index;
      if (char === '.') index += 1;
      while (index < input.length && /[0-9_]/u.test(input[index])) index += 1;
      if (input[index] === '.') {
        index += 1;
        while (index < input.length && /[0-9_]/u.test(input[index])) index += 1;
      }
      if (input[index] === 'e' || input[index] === 'E') {
        index += 1;
        if (input[index] === '+' || input[index] === '-') index += 1;
        while (index < input.length && /[0-9_]/u.test(input[index])) index += 1;
      }
      const raw = input.slice(start, index).replaceAll('_', '');
      const value = Number(raw);
      if (!Number.isFinite(value)) throw new TemplateParseError(`Invalid number literal: ${raw}`, start);
      tokens.push({ kind: 'number', value, index: start });
      continue;
    }

    if (/[A-Za-z_]/u.test(char)) {
      const start = index;
      index += 1;
      while (index < input.length && /[A-Za-z0-9_]/u.test(input[index])) index += 1;
      tokens.push({ kind: 'ident', value: input.slice(start, index), index: start });
      continue;
    }

    const operator = OPERATORS.find((candidate) => input.startsWith(candidate, index));
    if (operator) {
      tokens.push({ kind: 'op', value: operator, index });
      index += operator.length;
      continue;
    }

    throw new TemplateParseError(`Unexpected character "${char}"`, index);
  }

  tokens.push({ kind: 'eof', value: '', index });
  return tokens;
}

/* --------------------------- Expression parser -------------------------- */

const BINARY_PRECEDENCE: Record<string, number | undefined> = {
  '??': 1,
  '||': 2,
  or: 2,
  '&&': 3,
  and: 3,
  '==': 4,
  '!=': 4,
  '===': 4,
  '!==': 4,
  '<': 5,
  '<=': 5,
  '>': 5,
  '>=': 5,
  '+': 6,
  '-': 6,
  '*': 7,
  '/': 7,
  '%': 7,
};

class ExpressionParser {
  private position = 0;
  private nesting = 0;
  private unaryNesting = 0;
  private readonly depthLimit: number;
  private readonly parserNestingLimit: number;
  private readonly input: string;
  private readonly tokens: ExpressionToken[];

  constructor(
    input: string,
    tokens: ExpressionToken[],
    quota: NormalizedQuota,
    baseDepth = 0,
  ) {
    this.input = input;
    this.tokens = tokens;
    this.depthLimit = Math.max(1, quota.maxAstDepth - baseDepth);
    this.parserNestingLimit = quota.maxParserNesting;
  }

  parseRoot(): Expression {
    const node = this.parseExpression();
    this.expectKind('eof');
    return node;
  }

  parseForHeader(): { valueBinding: string; keyBinding: string | null; collection: Expression } {
    let parenthesized = false;
    if (this.matchOp('(')) parenthesized = true;

    const valueBinding = this.expectIdentifierName('Expected loop variable');
    let keyBinding: string | null = null;
    if (this.matchOp(',')) {
      keyBinding = this.expectIdentifierName('Expected loop key/index variable');
    }
    if (parenthesized) this.expectOp(')');

    for (const binding of [valueBinding, keyBinding]) {
      if (binding && BLOCKED_PROPERTIES.has(binding)) {
        throw this.parseError(`"${binding}" is reserved and cannot be used as a loop variable`);
      }
    }

    const inToken = this.peek();
    if (inToken.kind !== 'ident' || inToken.value !== 'in') {
      throw this.parseError('Expected "in" in for tag', inToken.index);
    }
    this.position += 1;

    const collection = this.parseExpression();
    this.expectKind('eof');
    return { valueBinding, keyBinding, collection };
  }

  private parseError(message: string, index = this.peek().index): TemplateParseError {
    return new TemplateParseError(message, index, this.input);
  }

  private peek(offset = 0): ExpressionToken {
    return this.tokens[Math.min(this.position + offset, this.tokens.length - 1)];
  }

  private next(): ExpressionToken {
    const token = this.peek();
    if (token.kind !== 'eof') this.position += 1;
    return token;
  }

  private matchOp(value: string): boolean {
    const token = this.peek();
    if (token.kind === 'op' && token.value === value) {
      this.position += 1;
      return true;
    }
    return false;
  }

  private expectOp(value: string): void {
    const token = this.peek();
    if (token.kind !== 'op' || token.value !== value) {
      throw this.parseError(`Expected "${value}" but found "${String(token.value)}"`);
    }
    this.position += 1;
  }

  private expectKind(kind: ExpressionTokenKind): ExpressionToken {
    const token = this.peek();
    if (token.kind !== kind) {
      throw this.parseError(`Expected ${kind} but found "${String(token.value)}"`);
    }
    this.position += 1;
    return token;
  }

  private expectIdentifierName(message: string): string {
    const token = this.peek();
    if (token.kind !== 'ident') throw this.parseError(message);
    this.position += 1;
    return String(token.value);
  }

  private checkDepth(depth: number): void {
    if (depth > this.depthLimit) {
      throw new TemplateQuotaError(
        'maxAstDepth',
        `Expression AST depth ${depth} exceeds limit ${this.depthLimit}`,
        this.depthLimit,
        depth,
      );
    }
  }

  private enterExpression(): void {
    this.nesting += 1;
    if (this.nesting > this.parserNestingLimit) {
      throw new TemplateQuotaError(
        'maxAstDepth',
        `Expression parser nesting exceeds limit ${this.parserNestingLimit}`,
        this.parserNestingLimit,
        this.nesting,
      );
    }
  }

  parseExpression(): Expression {
    this.enterExpression();
    try {
      return this.parseConditional();
    } finally {
      this.nesting -= 1;
    }
  }

  private parseConditional(): Expression {
    const test = this.parseBinary(0);
    if (!this.matchOp('?')) return test;
    const consequent = this.parseExpression();
    this.expectOp(':');
    const alternate = this.parseExpression();
    const depth = 1 + Math.max(test.depth, consequent.depth, alternate.depth);
    this.checkDepth(depth);
    return { kind: 'Conditional', depth, test, consequent, alternate };
  }

  private parseFilter(): Expression {
    let input = this.parseUnary();

    while (this.peek().kind === 'op' && this.peek().value === '|') {
      this.next();
      const nameToken = this.expectKind('ident');
      const name = String(nameToken.value);
      let args: Expression[] = [];

      if (this.matchOp('(')) {
        args = this.parseArgumentList(')');
        this.expectOp(')');
      } else if (this.matchOp(':')) {
        args = [this.parseExpression()];
        while (this.matchOp(',')) args.push(this.parseExpression());
      }

      const depth = 1 + Math.max(input.depth, ...args.map((arg) => arg.depth), 0);
      this.checkDepth(depth);
      input = { kind: 'Filter', depth, input, name, args };
    }

    return input;
  }

  private parseArgumentList(closing: ')' | ']'): Expression[] {
    const args: Expression[] = [];
    if (this.peek().kind === 'op' && this.peek().value === closing) return args;
    args.push(this.parseExpression());
    while (this.matchOp(',')) {
      if (this.peek().kind === 'op' && this.peek().value === closing) break;
      args.push(this.parseExpression());
    }
    return args;
  }

  private parseBinary(minPrecedence: number): Expression {
    let left = this.parseFilter();

    for (;;) {
      const token = this.peek();
      let operator = token.kind === 'op' ? String(token.value) : '';
      if (token.kind === 'ident' && (token.value === 'and' || token.value === 'or')) {
        operator = String(token.value);
      }
      const precedence = BINARY_PRECEDENCE[operator];
      if (precedence === undefined || precedence < minPrecedence) break;

      this.next();
      const right = this.parseBinary(precedence + 1);
      const depth = 1 + Math.max(left.depth, right.depth);
      this.checkDepth(depth);

      if (operator === '&&' || operator === '||' || operator === '??' || operator === 'and' || operator === 'or') {
        const logicalOperator =
          operator === 'and' ? '&&' : operator === 'or' ? '||' : (operator as '&&' | '||' | '??');
        left = { kind: 'Logical', depth, operator: logicalOperator, left, right };
      } else {
        left = { kind: 'Binary', depth, operator, left, right };
      }
    }

    return left;
  }

  private parseUnary(): Expression {
    const token = this.peek();
    const isNot = token.kind === 'ident' && token.value === 'not';
    if (
      isNot ||
      (token.kind === 'op' && (token.value === '!' || token.value === '-' || token.value === '+'))
    ) {
      this.next();
      this.unaryNesting += 1;
      if (this.unaryNesting > this.parserNestingLimit) {
        throw new TemplateQuotaError(
          'maxAstDepth',
          `Unary parser nesting exceeds limit ${this.parserNestingLimit}`,
          this.parserNestingLimit,
          this.unaryNesting,
        );
      }
      try {
        const argument = this.parseUnary();
        const depth = argument.depth + 1;
        this.checkDepth(depth);
        return { kind: 'Unary', depth, operator: isNot ? '!' : (token.value as '!' | '-' | '+'), argument };
      } finally {
        this.unaryNesting -= 1;
      }
    }
    return this.parsePostfix(this.parsePrimary());
  }

  private parsePostfix(initial: Expression): Expression {
    let node = initial;

    for (;;) {
      if (this.matchOp('.')) {
        const name = this.expectIdentifierName('Expected property name after "."');
        const key: LiteralExpression = { kind: 'Literal', depth: 1, value: name };
        const depth = 1 + Math.max(node.depth, key.depth);
        this.checkDepth(depth);
        node = { kind: 'Member', depth, object: node, key, computed: false };
        continue;
      }

      if (this.matchOp('[')) {
        const key = this.parseExpression();
        this.expectOp(']');
        const depth = 1 + Math.max(node.depth, key.depth);
        this.checkDepth(depth);
        node = { kind: 'Member', depth, object: node, key, computed: true };
        continue;
      }

      if (this.matchOp('(')) {
        const args = this.parseArgumentList(')');
        this.expectOp(')');
        const depth = 1 + Math.max(node.depth, ...args.map((arg) => arg.depth), 0);
        this.checkDepth(depth);
        node = { kind: 'Call', depth, callee: node, args };
        continue;
      }

      if (this.peek().kind === 'op' && this.peek().value === '?.') {
        throw this.parseError('Optional chaining is not enabled in the restricted expression grammar');
      }

      if (this.peek().kind === 'op' && this.peek().value === '=>') {
        throw this.parseError('Arrow/function expressions are not allowed');
      }

      return node;
    }
  }

  private parsePrimary(): Expression {
    const token = this.peek();

    if (token.kind === 'number') {
      this.next();
      return { kind: 'Literal', depth: 1, value: token.value as number };
    }

    if (token.kind === 'string') {
      this.next();
      return { kind: 'Literal', depth: 1, value: token.value as string };
    }

    if (token.kind === 'ident') {
      this.next();
      const name = String(token.value);
      if (name === 'true') return { kind: 'Literal', depth: 1, value: true };
      if (name === 'false') return { kind: 'Literal', depth: 1, value: false };
      if (name === 'null') return { kind: 'Literal', depth: 1, value: null };
      if (name === 'undefined') return { kind: 'Literal', depth: 1, value: undefined };
      if (name === 'new' || name === 'function' || name === 'class' || name === 'this') {
        throw this.parseError(`"${name}" is not allowed in template expressions`, token.index);
      }
      return { kind: 'Identifier', depth: 1, name };
    }

    if (this.matchOp('(')) {
      const expression = this.parseExpression();
      this.expectOp(')');
      return expression;
    }

    if (this.matchOp('[')) {
      const elements = this.parseArgumentList(']');
      this.expectOp(']');
      const depth = 1 + Math.max(...elements.map((element) => element.depth), 0);
      this.checkDepth(depth);
      return { kind: 'Array', depth, elements };
    }

    if (this.matchOp('{')) {
      const properties: ObjectPropertyNode[] = [];
      if (!this.matchOp('}')) {
        do {
          const keyToken = this.peek();
          let key: string;
          if (keyToken.kind === 'ident' || keyToken.kind === 'string') {
            this.next();
            key = String(keyToken.value);
          } else if (keyToken.kind === 'number') {
            this.next();
            key = String(keyToken.value);
          } else {
            throw this.parseError('Expected object property name');
          }

          if (this.matchOp(':')) {
            const value = this.parseExpression();
            const depth = 1 + value.depth;
            this.checkDepth(depth);
            properties.push({ kind: 'ObjectProperty', depth, key, value });
          } else {
            throw this.parseError('Expected ":" after object property name');
          }
        } while (this.matchOp(',') && this.peek().kind !== 'op');
        this.expectOp('}');
      }
      const depth = 1 + Math.max(...properties.map((property) => property.depth), 0);
      this.checkDepth(depth);
      return { kind: 'Object', depth, properties };
    }

    if (token.kind === 'eof') throw this.parseError('Unexpected end of expression', token.index);
    throw this.parseError(`Unexpected token "${String(token.value)}"`, token.index);
  }
}

export function parseExpression(
  input: string,
  options?: TemplateOptionInput | number,
  baseDepth = 0,
): Expression {
  const normalized = normalizeOptions(typeof options === 'number' ? { maxAstDepth: options } : options);
  const tokens = tokenizeExpression(input, normalized);
  const parser = new ExpressionParser(input, tokens, normalized.quota, baseDepth);
  return parser.parseRoot();
}

function parseForHeader(
  input: string,
  quota: NormalizedQuota,
): { valueBinding: string; keyBinding: string | null; collection: Expression } {
  const tokens = tokenizeExpression(input, quota.maxExpressionLength);
  const parser = new ExpressionParser(input, tokens, quota, 0);
  return parser.parseForHeader();
}

/* ----------------------------- Template parser -------------------------- */

const IF_STOPS = ['elseif', 'elif', 'else', 'endif'];

class TemplateParser {
  private position = 0;
  private readonly tokens: TemplateToken[];
  private readonly source: string;
  private readonly quota: NormalizedQuota;

  constructor(
    tokens: TemplateToken[],
    source: string,
    quota: NormalizedQuota,
  ) {
    this.tokens = tokens;
    this.source = source;
    this.quota = quota;
  }

  parse(): TemplateNode[] {
    return this.parseSequence([], 1);
  }

  private peek(): TemplateToken {
    return this.tokens[Math.min(this.position, this.tokens.length - 1)];
  }

  private next(): TemplateToken {
    const token = this.peek();
    if (token.kind !== 'eof') this.position += 1;
    return token;
  }

  private parseError(message: string, token = this.peek()): TemplateParseError {
    return new TemplateParseError(message, token.start, this.source);
  }

  private parseSequence(stopNames: string[], depth: number): TemplateNode[] {
    if (depth > this.quota.maxAstDepth) {
      throw new TemplateQuotaError(
        'maxAstDepth',
        `Template AST depth ${depth} exceeds limit ${this.quota.maxAstDepth}`,
        this.quota.maxAstDepth,
        depth,
      );
    }

    const nodes: TemplateNode[] = [];

    for (;;) {
      const token = this.peek();
      if (token.kind === 'eof') {
        if (stopNames.length > 0) throw this.parseError(`Unexpected end; expected ${stopNames.join(' or ')}`);
        return nodes;
      }

      if (token.kind === 'block' && token.name && stopNames.includes(token.name)) {
        return nodes;
      }

      this.next();

      if (token.kind === 'text') {
        nodes.push({ kind: 'Text', depth, value: token.value, start: token.start, end: token.end });
        continue;
      }

      if (token.kind === 'comment') continue;

      if (token.kind === 'output') {
        if (!token.value) throw this.parseError('Empty output expression', token);
        const expression = parseExpression(token.value, { quotas: this.quota }, depth);
        nodes.push({
          kind: 'Output',
          depth,
          expression,
          raw: false,
          start: token.start,
          end: token.end,
        });
        continue;
      }

      if (token.kind === 'block') {
        if (token.name === 'if') {
          nodes.push(this.parseIf(token, depth));
          continue;
        }
        if (token.name === 'for') {
          nodes.push(this.parseFor(token, depth));
          continue;
        }
        if (['else', 'elseif', 'elif', 'endif', 'endfor'].includes(token.name ?? '')) {
          throw this.parseError(`Unexpected ${token.name} tag`, token);
        }
        throw this.parseError(`Unknown block tag "${token.name}"`, token);
      }
    }
  }

  private parseIf(openToken: TemplateToken, depth: number): IfNode {
    const branches: IfBranch[] = [];
    let current = openToken;

    for (;;) {
      if (current.name === 'if' || current.name === 'elseif' || current.name === 'elif') {
        if (!current.argument) throw this.parseError(`${current.name} requires a condition`, current);
        const test = parseExpression(current.argument, { quotas: this.quota }, depth);
        const body = this.parseSequence(IF_STOPS, depth + 1);
        const stop = this.next();
        branches.push({ test, nodes: body });

        if (stop.name === 'endif') break;

        if (stop.name === 'else') {
          if (stop.argument) throw this.parseError('else does not take a condition', stop);
          const elseBody = this.parseSequence(['endif'], depth + 1);
          const end = this.next();
          if (end.name !== 'endif') throw this.parseError('Expected endif', end);
          branches.push({ test: null, nodes: elseBody });
          break;
        }

        current = stop;
        continue;
      }

      throw this.parseError('Malformed if tag', current);
    }

    return { kind: 'If', depth, branches, start: openToken.start, end: this.peek().start };
  }

  private parseFor(token: TemplateToken, depth: number): ForNode {
    if (!token.argument) throw this.parseError('for tag requires "x in collection"', token);
    let header: ReturnType<typeof parseForHeader>;
    try {
      header = parseForHeader(token.argument, this.quota);
    } catch (error) {
      if (error instanceof TemplateParseError && error.index !== undefined && token.start !== undefined) {
        throw new TemplateParseError(error.message, token.start + 2 + (error.index ?? 0), this.source);
      }
      throw error;
    }

    const body = this.parseSequence(['endfor'], depth + 1);
    const end = this.next();
    if (end.name !== 'endfor') throw this.parseError('Expected endfor', end);

    return {
      kind: 'For',
      depth,
      valueBinding: header.valueBinding,
      keyBinding: header.keyBinding,
      collection: header.collection,
      nodes: body,
      start: token.start,
      end: end.end,
    };
  }
}

export function parseTemplate(
  source: string,
  options?: TemplateOptionInput | number,
): TemplateNode[] {
  const normalized = normalizeOptions(typeof options === 'number' ? { maxAstDepth: options } : options);
  const tokens = tokenizeTemplate(source, normalized);
  const parser = new TemplateParser(tokens, source, normalized.quota);
  return parser.parse();
}

/* ------------------------------ Sandbox data ---------------------------- */

const BLOCKED_PROPERTIES = new Set([
  '__proto__',
  'constructor',
  'prototype',
  '__defineGetter__',
  '__defineSetter__',
  '__lookupGetter__',
  '__lookupSetter__',
]);

function normalizePropertyKey(key: unknown): string | number {
  if (typeof key === 'string') return key;
  if (typeof key === 'number' || typeof key === 'boolean' || typeof key === 'bigint') {
    return String(key);
  }
  if (key === null) return 'null';
  if (key === undefined) return 'undefined';
  throw new TemplateSecurityError('Dynamic property key must be a string, number, boolean, null, or undefined');
}

function isSafeLengthTarget(target: unknown): boolean {
  return Array.isArray(target) || typeof target === 'string';
}

export function readSecureProperty(target: unknown, rawKey: unknown): unknown {
  const key = normalizePropertyKey(rawKey);
  const keyText = String(key);

  if (BLOCKED_PROPERTIES.has(keyText)) {
    throw new TemplateSecurityError(`Access to blocked property "${keyText}" is forbidden`, {
      property: keyText,
    });
  }

  if (target === null || target === undefined) return undefined;

  if (typeof target === 'function') {
    throw new TemplateSecurityError('Function objects are not exposed to templates');
  }

  const owner = typeof target === 'object' ? target : Object(target);

  if (Object.hasOwn(owner, key)) {
    const descriptor = Object.getOwnPropertyDescriptor(owner, key);
    if (!descriptor) return undefined;

    if (
      keyText === 'length' &&
      isSafeLengthTarget(target) &&
      typeof descriptor.value === 'number'
    ) {
      return descriptor.value;
    }

    if (!descriptor.enumerable) {
      throw new TemplateSecurityError(`Property "${keyText}" is not an enumerable own property`, {
        property: keyText,
      });
    }

    if (descriptor.get || descriptor.set || !('value' in descriptor)) {
      throw new TemplateSecurityError(`Accessor property "${keyText}" is not callable from templates`, {
        property: keyText,
      });
    }

    if (typeof descriptor.value === 'function') {
      throw new TemplateSecurityError(`Function-valued property "${keyText}" is not exposed`, {
        property: keyText,
      });
    }

    return descriptor.value;
  }

  if (key in owner) {
    throw new TemplateSecurityError(`Access to inherited prototype member "${keyText}" is forbidden`, {
      property: keyText,
    });
  }

  return undefined;
}

class Scope {
  private readonly frames: Array<Map<string, unknown>> = [];
  private readonly root: Record<string, unknown>;
  private readonly strictVariables: boolean;

  constructor(root: Record<string, unknown>, strictVariables = false) {
    this.root = root;
    this.strictVariables = strictVariables;
  }

  push(bindings: Record<string, unknown>): void {
    const frame = new Map<string, unknown>();
    for (const [key, value] of Object.entries(bindings)) frame.set(key, value);
    this.frames.push(frame);
  }

  pop(): void {
    this.frames.pop();
  }

  get(name: string): unknown {
    for (let i = this.frames.length - 1; i >= 0; i -= 1) {
      const frame = this.frames[i];
      if (frame.has(name)) {
        const value = frame.get(name);
        if (typeof value === 'function') {
          throw new TemplateSecurityError(`Function binding "${name}" is not callable or renderable`);
        }
        return value;
      }
    }

    if (this.root === null || typeof this.root !== 'object') {
      if (this.strictVariables) {
        throw new TemplateRuntimeError(`Unknown template variable "${name}"`);
      }
      return undefined;
    }
    const value = readSecureProperty(this.root, name);
    if (this.strictVariables && value === undefined && !Object.hasOwn(this.root, name)) {
      throw new TemplateRuntimeError(`Unknown template variable "${name}"`);
    }
    return value;
  }
}

class RenderState {
  steps = 0;
  iterations = 0;
  outputBytes = 0;
  private readonly chunks: string[] = [];
  readonly options: NormalizedOptions;

  constructor(options: NormalizedOptions) {
    this.options = options;
  }

  tick(weight = 1): void {
    this.steps += weight;
    if (this.steps > this.options.quota.maxEvaluationSteps) {
      throw new TemplateQuotaError(
        'maxEvaluationSteps',
        `Evaluation step budget exhausted after ${this.steps} steps`,
        this.options.quota.maxEvaluationSteps,
        this.steps,
      );
    }
  }

  nextIteration(): void {
    this.iterations += 1;
    if (this.iterations > this.options.quota.maxLoopIterations) {
      throw new TemplateQuotaError(
        'maxLoopIterations',
        `Loop iteration limit exceeded after ${this.iterations} iterations`,
        this.options.quota.maxLoopIterations,
        this.iterations,
      );
    }
    this.tick();
  }

  append(value: string): void {
    const bytes = utf8ByteLength(value);
    if (this.outputBytes + bytes > this.options.quota.maxOutputBytes) {
      throw new TemplateQuotaError(
        'maxOutputBytes',
        `Output would exceed ${this.options.quota.maxOutputBytes} UTF-8 bytes`,
        this.options.quota.maxOutputBytes,
        this.outputBytes + bytes,
      );
    }
    this.outputBytes += bytes;
    this.chunks.push(value);
  }

  output(): string {
    return this.chunks.join('');
  }
}

/* ------------------------------ Evaluator -------------------------------- */

function isThenable(value: unknown): boolean {
  return (
    value !== null &&
    (typeof value === 'object' || typeof value === 'function') &&
    typeof (value as { then?: unknown }).then === 'function'
  );
}

function callRegisteredFilter(
  state: RenderState,
  name: string,
  input: unknown,
  args: unknown[],
  pipeInput: boolean,
): unknown {
  const filters = state.options.filters;
  if (!Object.hasOwn(filters, name) || typeof filters[name] !== 'function') {
    throw new TemplateSecurityError(`Filter "${name}" is not in the explicit host whitelist`, {
      filter: name,
    });
  }
  const filter = filters[name] as FilterFunction;

  let result: unknown;
  try {
    if (pipeInput) {
      result = filter(input, ...args);
    } else {
      result = input === undefined ? filter(...args) : filter(input, ...args);
    }
  } catch (error) {
    throw new TemplateRuntimeError(
      `Registered filter "${name}" failed: ${error instanceof Error ? error.message : String(error)}`,
    );
  }

  if (isThenable(result)) {
    throw new TemplateRuntimeError(`Filter "${name}" returned a Promise/thenable; only sync filters are supported`);
  }
  return result;
}

function isTruthy(value: unknown): boolean {
  if (value === false || value === null || value === undefined || value === 0 || value === '') return false;
  if (typeof value === 'number' && Number.isNaN(value)) return false;
  return true;
}

function toNumber(value: unknown): number {
  if (typeof value === 'number') return value;
  if (typeof value === 'boolean') return value ? 1 : 0;
  if (typeof value === 'string' && value.trim() !== '') {
    const number = Number(value);
    if (Number.isFinite(number)) return number;
  }
  if (value === null) return 0;
  throw new TemplateRuntimeError(`Expected numeric value, received ${describeType(value)}`);
}

function describeType(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  return typeof value;
}

function toPrimitiveString(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') {
    return String(value);
  }
  if (value === null) return 'null';
  if (value === undefined) return 'undefined';
  throw new TemplateRuntimeError(`Cannot implicitly convert ${describeType(value)} to string`);
}

function safeLooseEquals(left: unknown, right: unknown): boolean {
  if (left === null || left === undefined) return right === null || right === undefined;
  if (right === null || right === undefined) return false;

  if (typeof left === typeof right) return left === right;

  if (typeof left === 'number' && typeof right === 'string') {
    let number: number;
    try {
      number = toNumber(right);
    } catch {
      return false;
    }
    return left === number;
  }
  if (typeof right === 'number' && typeof left === 'string') return safeLooseEquals(right, left);

  if (typeof left === 'boolean') return safeLooseEquals(left ? 1 : 0, right);
  if (typeof right === 'boolean') return safeLooseEquals(left, right ? 1 : 0);

  if (typeof left === 'object' || typeof right === 'object') {
    throw new TemplateRuntimeError('Loose equality cannot coerce objects; use strict equality or a filter');
  }

  return left === right;
}

function compareOrdered(left: unknown, right: unknown, operator: string): boolean {
  let result: boolean;
  if (typeof left === 'number' && typeof right === 'number') {
    result = operator === '<' ? left < right : operator === '<=' ? left <= right : operator === '>' ? left > right : left >= right;
  } else if (typeof left === 'string' && typeof right === 'string') {
    result = operator === '<' ? left < right : operator === '<=' ? left <= right : operator === '>' ? left > right : left >= right;
  } else {
    throw new TemplateRuntimeError(`Operator ${operator} requires two numbers or two strings`);
  }
  return result;
}

function renderOutputValue(value: unknown, state: RenderState): string {
  return stringifyOutput(value, state, 0, new WeakSet<object>());
}

function stringifyOutput(
  value: unknown,
  state: RenderState,
  depth: number,
  seen: WeakSet<object>,
): string {
  state.tick();
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') {
    return String(value);
  }
  if (typeof value === 'function' || typeof value === 'symbol') {
    throw new TemplateSecurityError('Function or symbol values cannot be rendered');
  }
  if (depth > state.options.quota.maxAstDepth) {
    throw new TemplateQuotaError('maxAstDepth', 'Output value nesting exceeds AST depth quota', state.options.quota.maxAstDepth, depth);
  }
  if (seen.has(value as object)) throw new TemplateRuntimeError('Circular value cannot be rendered');
  seen.add(value as object);

  try {
    if (Array.isArray(value)) {
      const parts: string[] = [];
      for (let index = 0; index < value.length; index += 1) {
        state.tick();
        const item = readSecureProperty(value, index);
        parts.push(item === undefined || item === null ? 'null' : stringifyOutput(item, state, depth + 1, seen));
      }
      return `[${parts.join(',')}]`;
    }

    const record = value as Record<string, unknown>;
    const parts: string[] = [];
    for (const key of Object.keys(record)) {
      state.tick();
      const item = readSecureProperty(record, key);
      parts.push(`${quoteJsonString(key)}:${stringifyOutput(item, state, depth + 1, seen)}`);
    }
    return `{${parts.join(',')}}`;
  } finally {
    seen.delete(value as object);
  }
}

function evalExpression(node: Expression, scope: Scope, state: RenderState): unknown {
  state.tick();

  switch (node.kind) {
    case 'Literal':
      return node.value;

    case 'Identifier':
      return scope.get(node.name);

    case 'Member': {
      const target = evalExpression(node.object, scope, state);
      const key = evalExpression(node.key, scope, state);
      return readSecureProperty(target, key);
    }

    case 'Array':
      return node.elements.map((element) => evalExpression(element, scope, state));

    case 'Object': {
      const result = Object.create(null) as Record<string, unknown>;
      for (const property of node.properties) {
        if (BLOCKED_PROPERTIES.has(property.key)) {
          throw new TemplateSecurityError(`Cannot construct blocked property "${property.key}"`);
        }
        result[property.key] = evalExpression(property.value, scope, state);
      }
      return result;
    }

    case 'Unary': {
      const value = evalExpression(node.argument, scope, state);
      if (node.operator === '!') return !isTruthy(value);
      const number = toNumber(value);
      return node.operator === '-' ? -number : number;
    }

    case 'Binary': {
      const left = evalExpression(node.left, scope, state);
      const right = evalExpression(node.right, scope, state);
      switch (node.operator) {
        case '+':
          if (typeof left === 'string' || typeof right === 'string') {
            return toPrimitiveString(left) + toPrimitiveString(right);
          }
          return toNumber(left) + toNumber(right);
        case '-':
          return toNumber(left) - toNumber(right);
        case '*':
          return toNumber(left) * toNumber(right);
        case '/':
          return toNumber(left) / toNumber(right);
        case '%':
          return toNumber(left) % toNumber(right);
        case '==':
          return safeLooseEquals(left, right);
        case '!=':
          return !safeLooseEquals(left, right);
        case '===':
          return left === right;
        case '!==':
          return left !== right;
        case '<':
          return compareOrdered(left, right, '<');
        case '<=':
          return compareOrdered(left, right, '<=');
        case '>':
          return compareOrdered(left, right, '>');
        case '>=':
          return compareOrdered(left, right, '>=');
        default:
          throw new TemplateParseError(`Unsupported operator "${node.operator}"`);
      }
    }

    case 'Logical': {
      const left = evalExpression(node.left, scope, state);
      if (node.operator === '&&' && !isTruthy(left)) return left;
      if (node.operator === '||' && isTruthy(left)) return left;
      if (node.operator === '??' && left !== null && left !== undefined) return left;
      return evalExpression(node.right, scope, state);
    }

    case 'Conditional': {
      const test = evalExpression(node.test, scope, state);
      return evalExpression(isTruthy(test) ? node.consequent : node.alternate, scope, state);
    }

    case 'Filter': {
      const filterInput = evalExpression(node.input, scope, state);
      const args = node.args.map((arg) => evalExpression(arg, scope, state));
      return callRegisteredFilter(state, node.name, filterInput, args, true);
    }

    case 'Call': {
      if (
        node.callee.kind === 'Identifier' &&
        Object.hasOwn(state.options.filters, node.callee.name) &&
        typeof state.options.filters[node.callee.name] === 'function'
      ) {
        const args = node.args.map((arg) => evalExpression(arg, scope, state));
        return callRegisteredFilter(state, node.callee.name, undefined, args, false);
      }
      throw new TemplateSecurityError(
        'Direct function and method calls are forbidden; register and use a named filter instead',
      );
    }

    default: {
      const unknownNode = node as { kind?: string };
      throw new TemplateParseError(`Unknown AST node "${unknownNode.kind ?? 'unknown'}"`);
    }
  }
}

function arrayIterationKeys(collection: unknown[]): string[] {
  return Object.keys(collection).filter((key) => /^(0|[1-9][0-9]*)$/u.test(key));
}

function evalFor(node: ForNode, scope: Scope, state: RenderState): void {
  const collection = evalExpression(node.collection, scope, state);
  if (collection === null || collection === undefined) return;
  if (typeof collection === 'function') {
    throw new TemplateSecurityError('Function collections are not exposed');
  }
  if (typeof collection !== 'object') {
    throw new TemplateRuntimeError(`for loop requires an array or object, received ${describeType(collection)}`);
  }

  const isArray = Array.isArray(collection);
  const keys = isArray ? arrayIterationKeys(collection) : Object.keys(collection as Record<string, unknown>);
  const length = keys.length;

  keys.forEach((key, index) => {
    state.nextIteration();
    const value = readSecureProperty(collection, key);
    const numericIndex = isArray ? Number(key) : index;
    const loop = {
      index: numericIndex,
      index0: numericIndex,
      index1: numericIndex + 1,
      first: index === 0,
      last: index === length - 1,
      length,
      key: isArray ? numericIndex : key,
    };

    const bindings: Record<string, unknown> = {
      [node.valueBinding]: value,
      loop,
      forloop: loop,
    };
    if (node.keyBinding) bindings[node.keyBinding] = isArray ? numericIndex : key;

    scope.push(bindings);
    try {
      for (const child of node.nodes) evalTemplateNode(child, scope, state);
    } finally {
      scope.pop();
    }
  });
}

function evalTemplateNode(node: TemplateNode, scope: Scope, state: RenderState): void {
  state.tick();

  switch (node.kind) {
    case 'Text':
      state.append(node.value);
      return;

    case 'Output': {
      const value = evalExpression(node.expression, scope, state);
      let text = renderOutputValue(value, state);
      if (state.options.escape) text = state.options.escape(text);
      state.append(text);
      return;
    }

    case 'If': {
      for (const branch of node.branches) {
        if (branch.test === null || isTruthy(evalExpression(branch.test, scope, state))) {
          for (const child of branch.nodes) evalTemplateNode(child, scope, state);
          return;
        }
      }
      return;
    }

    case 'For':
      evalFor(node, scope, state);
      return;

    default: {
      const unknownNode = node as { kind?: string };
      throw new TemplateParseError(`Unknown template AST node "${unknownNode.kind ?? 'unknown'}"`);
    }
  }
}

export function renderAst(
  ast: TemplateNode[],
  context: Record<string, unknown> | null | undefined,
  options?: SafeTemplateOptions | NormalizedOptions,
): string {
  const normalized =
    options && 'quota' in options && 'filters' in options
      ? (options as NormalizedOptions)
      : normalizeOptions(options as SafeTemplateOptions | QuotaOptions | undefined);
  const root = (context ?? Object.create(null)) as Record<string, unknown>;
  const scope = new Scope(root, normalized.strictVariables);
  const state = new RenderState(normalized);

  for (const node of ast) evalTemplateNode(node, scope, state);
  return state.output();
}

export function renderTemplate(
  source: string,
  context?: Record<string, unknown> | null,
  options?: TemplateOptionInput,
): string {
  const normalized = normalizeOptions(options);
  const ast = parseTemplate(source, normalized);
  return renderAst(ast, context, normalized);
}

/* --------------------------- Public convenience -------------------------- */

export class SafeTemplate {
  readonly source: string;
  readonly options: NormalizedOptions;
  readonly ast: TemplateNode[];

  constructor(source: string, options?: TemplateOptionInput);
  constructor(source: string, filters?: FilterMap, quotas?: QuotaOptions);
  constructor(
    source: string,
    optionsOrFilters: TemplateOptionInput | FilterMap = {},
    quotas?: QuotaOptions,
  ) {
    this.source = source;
    const input =
      isBareFilterMap(optionsOrFilters) && quotas !== undefined
        ? { filters: optionsOrFilters, quotas }
        : optionsOrFilters;
    this.options = normalizeOptions(input);
    this.ast = parseTemplate(source, this.options);
  }

  render(context?: Record<string, unknown> | null): string {
    return renderAst(this.ast, context, this.options);
  }

  execute(context?: Record<string, unknown> | null): string {
    return this.render(context);
  }

  run(context?: Record<string, unknown> | null): string {
    return this.render(context);
  }
}

export class SafeTemplateEngine {
  options: NormalizedOptions;

  static render(
    source: string,
    context?: Record<string, unknown> | null,
    options?: TemplateOptionInput,
  ): string {
    return renderTemplate(source, context, options);
  }

  static renderTemplate(
    source: string,
    context?: Record<string, unknown> | null,
    options?: TemplateOptionInput,
  ): string {
    return renderTemplate(source, context, options);
  }

  static compile(source: string, options?: TemplateOptionInput): SafeTemplate {
    return new SafeTemplate(source, options);
  }

  constructor(options?: SafeTemplateOptions);
  constructor(filters?: FilterMap, quotas?: QuotaOptions);
  constructor(optionsOrFilters: TemplateOptionInput = {}, quotas?: QuotaOptions) {
    if (isSafeTemplateOptions(optionsOrFilters)) {
      this.options = normalizeOptions(optionsOrFilters);
    } else if (isBareFilterMap(optionsOrFilters)) {
      this.options = normalizeOptions({ filters: optionsOrFilters, quotas });
    } else {
      this.options = normalizeOptions(optionsOrFilters);
    }
  }

  render(source: string, context?: Record<string, unknown> | null): string {
    return renderTemplate(source, context, this.options);
  }

  renderString(source: string, context?: Record<string, unknown> | null): string {
    return this.render(source, context);
  }

  execute(source: string, context?: Record<string, unknown> | null): string {
    return this.render(source, context);
  }

  run(source: string, context?: Record<string, unknown> | null): string {
    return this.render(source, context);
  }

  evaluate(source: string, context?: Record<string, unknown> | null): string {
    return this.render(source, context);
  }

  compile(source: string): SafeTemplate {
    return new SafeTemplate(source, this.options);
  }

  registerFilter(name: string, filter: FilterFunction): this {
    if (typeof filter !== 'function') throw new TypeError('Filter must be a function');
    this.options.filters[name] = filter;
    return this;
  }
}

export const TemplateSandbox = SafeTemplateEngine;
export const SafeTemplateRenderer = SafeTemplateEngine;
export const SecureTemplateEngine = SafeTemplateEngine;
export const TemplateRenderer = SafeTemplateEngine;
export const SafeTemplateParser = SafeTemplate;

export function createSafeRenderer(options?: SafeTemplateOptions): SafeTemplateEngine {
  return new SafeTemplateEngine(options);
}

export const SecureTemplate = SafeTemplate;
export const SafeTemplateSandbox = SafeTemplateEngine;
export const SecureTemplateSandbox = SafeTemplateEngine;
export const TemplateEngine = SafeTemplateEngine;

export function createTemplateSandbox(options?: SafeTemplateOptions): SafeTemplateEngine {
  return new SafeTemplateEngine(options);
}

export function createEngine(options?: SafeTemplateOptions): SafeTemplateEngine {
  return new SafeTemplateEngine(options);
}

export const createSandbox = createTemplateSandbox;

export function compileTemplate(
  source: string,
  options?: TemplateOptionInput,
): SafeTemplate {
  return new SafeTemplate(source, options);
}

export const createSafeTemplate = compileTemplate;
export const createTemplateEngine = createSafeRenderer;

export function renderSafeTemplate(
  source: string,
  context?: Record<string, unknown> | null,
  options?: TemplateOptionInput,
): string {
  return renderTemplate(source, context, options);
}

/* ------------------------------ Built-in filters ------------------------ */

function filterLength(value: unknown): number {
  if (typeof value === 'string' || Array.isArray(value)) return value.length;
  if (value && typeof value === 'object') return Object.keys(value as Record<string, unknown>).length;
  return 0;
}

function filterString(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  return primitiveOrJsonString(value);
}

export const builtInFilters: FilterMap = {
  upper: (value) => filterString(value).toUpperCase(),
  uppercase: (value) => filterString(value).toUpperCase(),
  lower: (value) => filterString(value).toLowerCase(),
  lowercase: (value) => filterString(value).toLowerCase(),
  trim: (value) => filterString(value).trim(),
  length: filterLength,
  count: filterLength,
  json: (value) => safeJsonStringify(value),
  keys: (value) => (value && typeof value === 'object' ? Object.keys(value as Record<string, unknown>) : []),
  first: (value) => (Array.isArray(value) ? value[0] : undefined),
  last: (value) => (Array.isArray(value) ? value[value.length - 1] : undefined),
  join: (value, separator = ', ') => {
    if (!Array.isArray(value)) return filterString(value);
    const sep = typeof separator === 'string' ? separator : ',';
    return value.map((item) => filterString(item)).join(sep);
  },
  default: (value, fallback) => {
    if (value === null || value === undefined || value === '') return fallback;
    return value;
  },
  add: (value, amount) => toNumber(value) + toNumber(amount),
};

Object.assign(SafeTemplateEngine, {
  SafeTemplate,
  SecureTemplate,
  SafeTemplateSandbox,
  SecureTemplateSandbox,
  TemplateEngine,
  TemplateSandbox,
  renderTemplate,
  renderSafeTemplate,
  compileTemplate,
  createTemplateSandbox,
  createEngine,
  createSandbox,
  parseTemplate,
  parseExpression,
  tokenizeTemplate,
  tokenizeExpression,
  builtInFilters,
  escapeHtml,
  SafeTemplateError,
  TemplateParseError,
  TemplateSecurityError,
  TemplateQuotaError,
  TemplateRuntimeError,
  TemplateError,
  ParseError,
  SecurityError,
  QuotaError,
  QuotaExceededError,
  DEFAULT_QUOTA,
});

export default SafeTemplateEngine;
