import type {
  BinaryExpr,
  Expr,
  FilterStep,
  JsonPathAst,
  JsonValue,
  PathExpr,
  PathVars,
  Step,
  Subscript,
  TriState,
  UnaryExpr,
} from './ast.ts';
import { isStructuralError, JsonPathError } from './errors.ts';
import { parseJsonPath as parseAst } from './parser.ts';

export interface EvalOptions {
  /** Values for named-variable references (`$name`). */
  readonly vars?: PathVars;
}

interface EvalContext {
  readonly ast: JsonPathAst;
  readonly root: JsonValue;
  current: JsonValue;
  readonly vars: PathVars;
  /** Defined only while evaluating array subscript bounds. */
  last: number | undefined;
}

type ComparisonOperator = '==' | '!=' | '<' | '<=' | '>' | '>=';

/** Recursively flatten every array encountered (lax auto-unwrap, all levels). */
function flattenArrays(values: readonly JsonValue[]): JsonValue[] {
  const out: JsonValue[] = [];
  const walk = (value: JsonValue): void => {
    if (Array.isArray(value)) {
      for (const element of value) walk(element);
    } else {
      out.push(value);
    }
  };
  for (const value of values) walk(value);
  return out;
}

function isObject(value: JsonValue): value is { [key: string]: JsonValue } {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

export function jsonTypeName(value: JsonValue): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (isObject(value)) return 'object';
  if (typeof value === 'boolean') return 'boolean';
  if (typeof value === 'string') return 'string';
  if (typeof value === 'number') return Number.isInteger(value) ? 'integer' : 'number';
  throw new JsonPathError('JSON_PATH_INTERNAL', 'value is not a JSON item');
}

export function deepEqual(left: JsonValue, right: JsonValue): boolean {
  if (left === null || right === null) return left === right;
  if (typeof left === 'number' && typeof right === 'number') return left === right;
  if (typeof left === 'string' || typeof left === 'boolean') return left === right;
  if (Array.isArray(left) && Array.isArray(right)) {
    if (left.length !== right.length) return false;
    return left.every((element, i) => deepEqual(element, right[i]));
  }
  if (isObject(left) && isObject(right)) {
    const leftKeys = Object.keys(left);
    const rightKeys = Object.keys(right);
    if (leftKeys.length !== rightKeys.length) return false;
    return leftKeys.every((key) => key in right && deepEqual(left[key], right[key]));
  }
  return false;
}

/** Recursive descendant enumeration in depth-first preorder (item first). */
function descendants(value: JsonValue): JsonValue[] {
  const out: JsonValue[] = [value];
  if (Array.isArray(value)) {
    for (const element of value) out.push(...descendants(element));
  } else if (isObject(value)) {
    for (const key of Object.keys(value)) out.push(...descendants(value[key]));
  }
  return out;
}

export class JsonPathEvaluator {
  private readonly ast: JsonPathAst;

  constructor(ast: JsonPathAst) {
    this.ast = ast;
  }

  query(document: JsonValue, options: EvalOptions = {}): JsonValue[] {
    const ctx: EvalContext = {
      ast: this.ast,
      root: document,
      current: document,
      vars: options.vars ?? {},
      last: undefined,
    };
    try {
      return this.evalPath(this.ast.expr, ctx);
    } catch (error) {
      if (
        this.ast.mode === 'lax' &&
        error instanceof JsonPathError &&
        isStructuralError(error.code)
      ) {
        return [];
      }
      throw error;
    }
  }

  /** Predicate-style entry: a single boolean is unwrapped, otherwise non-empty means true. */
  match(document: JsonValue, options: EvalOptions = {}): boolean {
    const result = this.query(document, options);
    if (result.length === 1 && typeof result[0] === 'boolean') return result[0];
    return result.length > 0;
  }

  private evalPath(expr: PathExpr, ctx: EvalContext): JsonValue[] {
    let seq: JsonValue[];
    switch (expr.base.type) {
      case 'root':
        seq = [ctx.root];
        break;
      case 'current':
        seq = [ctx.current];
        break;
      case 'variable':
        seq = [this.resolveVariable(expr.base.name, ctx, expr.base.pos)];
        break;
    }
    for (const step of expr.steps) {
      seq = this.evalStep(step, seq, ctx);
    }
    return seq;
  }

  private resolveVariable(name: string, ctx: EvalContext, pos: number): JsonValue {
    if (name === 'last') {
      if (ctx.last === undefined) {
        throw new JsonPathError(
          'JSON_PATH_LAST_OUTSIDE_SUBSCRIPT',
          '"last" can only be used inside an array subscript',
          pos,
        );
      }
      return ctx.last;
    }
    if (!(name in ctx.vars)) {
      throw new JsonPathError(
        'JSON_PATH_UNKNOWN_VARIABLE',
        `variable ${JSON.stringify(name)} is not defined`,
        pos,
      );
    }
    return ctx.vars[name];
  }

  private evalStep(step: Step, seq: readonly JsonValue[], ctx: EvalContext): JsonValue[] {
    const lax = this.ast.mode === 'lax';
    const out: JsonValue[] = [];

    const handleItem = (item: JsonValue, apply: (value: JsonValue) => JsonValue[]): void => {
      try {
        out.push(...apply(item));
      } catch (error) {
        if (lax && error instanceof JsonPathError && isStructuralError(error.code)) {
          return;
        }
        throw error;
      }
    };

    switch (step.type) {
      case 'member': {
        const items = lax ? flattenArrays(seq) : seq;
        for (const item of items) {
          handleItem(item, (value) => {
            if (!isObject(value)) {
              if (lax) return [];
              throw new JsonPathError(
                'JSON_PATH_MEMBER_OF_NON_OBJECT',
                `cannot access member ${JSON.stringify(step.key)} of a non-object ${jsonTypeName(value)}`,
                step.pos,
              );
            }
            if (!(step.key in value)) {
              if (lax) return [];
              throw new JsonPathError(
                'JSON_PATH_MEMBER_NOT_FOUND',
                `object has no member ${JSON.stringify(step.key)}`,
                step.pos,
              );
            }
            return [value[step.key]];
          });
        }
        return out;
      }

      case 'wildcardMember': {
        const items = lax ? flattenArrays(seq) : seq;
        for (const item of items) {
          handleItem(item, (value) => {
            if (!isObject(value)) {
              if (lax) return [];
              throw new JsonPathError(
                'JSON_PATH_MEMBER_OF_NON_OBJECT',
                `cannot apply wildcard member access to a non-object ${jsonTypeName(value)}`,
                step.pos,
              );
            }
            return Object.keys(value).map((key) => (value as Record<string, JsonValue>)[key]);
          });
        }
        return out;
      }

      case 'subscript': {
        // Arrays must stay intact for subscripting; lax scalar wrapping is
        // handled inside evalSubscripts, not by member-style flattening here.
        const items = seq;
        for (const item of items) {
          handleItem(item, (value) => this.evalSubscripts(step.subscripts, value, ctx, step.pos));
        }
        return out;
      }

      case 'recursiveDescendant': {
        for (const item of seq) out.push(...descendants(item));
        return out;
      }

      case 'filter':
        return this.evalFilterStep(step, seq, ctx);

      case 'method':
        return this.evalMethodStep(step, seq, lax, step.pos);
    }
  }

  private evalSubscripts(
    subscripts: readonly Subscript[],
    value: JsonValue,
    ctx: EvalContext,
    pos: number,
  ): JsonValue[] {
    const lax = this.ast.mode === 'lax';
    let array: JsonValue[];
    if (Array.isArray(value)) {
      array = value;
    } else if (lax) {
      // By the time a subscript step runs in lax mode, member-level unwrapping
      // has already flattened input arrays; a remaining non-array is a genuine
      // scalar, which SQL/JSON treats as a one-element array for subscripting.
      array = [value];
    } else {
      throw new JsonPathError(
        'JSON_PATH_SUBSCRIPT_OF_NON_ARRAY',
        `cannot apply array subscript to a non-array ${jsonTypeName(value)}`,
        pos,
      );
    }

    const out: JsonValue[] = [];
    const boundCtx: EvalContext = { ...ctx, last: array.length - 1 };

    const requireIntegerIndex = (raw: JsonValue, subPos: number): number => {
      if (typeof raw !== 'number' || !Number.isFinite(raw) || !Number.isInteger(raw)) {
        throw new JsonPathError(
          'JSON_PATH_INDEX_NOT_INTEGER',
          'array subscript must evaluate to an integer',
          subPos,
        );
      }
      return raw;
    };

    for (const subscript of subscripts) {
      if (subscript.kind === 'wildcardIndex') {
        out.push(...array);
        continue;
      }

      if (subscript.kind === 'index') {
        const raw = this.evalScalarExpr(subscript.value, boundCtx, lax);
        let index = requireIntegerIndex(raw, subscript.pos);
        if (index < 0) index += array.length;
        if (index < 0 || index >= array.length) {
          if (lax) continue;
          throw new JsonPathError(
            'JSON_PATH_INDEX_OUT_OF_BOUNDS',
            `array index ${index} is out of bounds for array of length ${array.length}`,
            subscript.pos,
          );
        }
        out.push(array[index]);
        continue;
      }

      const length = array.length;
      let start = 0;
      let end = length - 1;
      if (subscript.start !== null) {
        start = requireIntegerIndex(this.evalScalarExpr(subscript.start, boundCtx, lax), subscript.pos);
        if (start < 0) start += length;
      }
      if (subscript.end !== null) {
        end = requireIntegerIndex(this.evalScalarExpr(subscript.end, boundCtx, lax), subscript.pos);
        if (end < 0) end += length;
      }
      start = Math.max(0, Math.min(start, length - 1));
      end = Math.max(0, Math.min(end, length - 1));
      for (let index = start; index <= end; index += 1) out.push(array[index]);
    }
    return out;
  }

  private evalFilterStep(step: FilterStep, seq: readonly JsonValue[], ctx: EvalContext): JsonValue[] {
    const out: JsonValue[] = [];
    for (const item of seq) {
      const childCtx: EvalContext = { ...ctx, current: item };
      if (this.evalPredicate(step.predicate, childCtx) === 'TRUE') out.push(item);
    }
    return out;
  }

  private evalMethodStep(
    step: Extract<Step, { type: 'method' }>,
    seq: readonly JsonValue[],
    lax: boolean,
    pos: number,
  ): JsonValue[] {
    if (seq.length === 0) {
      if (lax) return [];
      throw new JsonPathError(
        'JSON_PATH_SINGLE_VALUE_EXPECTED',
        `method .${step.name}() requires a single SQL/JSON item but the sequence is empty`,
        pos,
      );
    }
    if (seq.length > 1) {
      throw new JsonPathError(
        'JSON_PATH_SINGLE_VALUE_EXPECTED',
        `method .${step.name}() requires a single SQL/JSON item but got ${seq.length} items`,
        pos,
      );
    }
    const value = seq[0];
    switch (step.name) {
      case 'type':
        return [jsonTypeName(value)];
      case 'size':
        if (Array.isArray(value)) return [value.length];
        if (isObject(value)) return [Object.keys(value).length];
        return [1];
      case 'double': {
        if (typeof value === 'number') return [value];
        if (lax) return [];
        throw new JsonPathError(
          'JSON_PATH_NUMERIC_EXPECTED',
          `.double() requires a numeric item but got ${jsonTypeName(value)}`,
          pos,
        );
      }
    }
  }

  private evalScalarExpr(expr: Expr, ctx: EvalContext, lax: boolean): JsonValue {
    const seq = this.evalExprSequence(expr, ctx);
    const items = lax ? flattenArrays(seq) : seq;
    if (items.length !== 1) {
      throw new JsonPathError(
        'JSON_PATH_SINGLE_VALUE_EXPECTED',
        `expected exactly one value but got ${items.length}`,
        expr.pos,
      );
    }
    return items[0];
  }

  private evalExprSequence(expr: Expr, ctx: EvalContext): JsonValue[] {
    switch (expr.type) {
      case 'literal':
        return [expr.value];
      case 'root':
        return [ctx.root];
      case 'current':
        return [ctx.current];
      case 'variable':
        return [this.resolveVariable(expr.name, ctx, expr.pos)];
      case 'path':
        return this.evalPath(expr, ctx);
      case 'unary':
        return [this.evalUnary(expr, ctx)];
      case 'binary':
        return [this.evalArithmetic(expr, ctx)];
      case 'exists':
      case 'likeRegex':
        throw new JsonPathError(
          'JSON_PATH_BOOLEAN_EXPECTED',
          'predicate-only expression used as a value',
          expr.pos,
        );
    }
  }

  private evalUnary(expr: UnaryExpr, ctx: EvalContext): JsonValue {
    const lax = this.ast.mode === 'lax';
    if (expr.operator === '!') {
      throw new JsonPathError(
        'JSON_PATH_BOOLEAN_EXPECTED',
        'logical negation is only valid in filter predicates',
        expr.pos,
      );
    }
    const value = this.evalScalarExpr(expr.argument, ctx, lax);
    if (typeof value !== 'number') {
      throw new JsonPathError(
        'JSON_PATH_NUMERIC_EXPECTED',
        `unary ${expr.operator} requires a numeric item but got ${jsonTypeName(value)}`,
        expr.pos,
      );
    }
    return expr.operator === '-' ? -value : value;
  }

  private evalArithmetic(expr: BinaryExpr, ctx: EvalContext): JsonValue {
    const lax = this.ast.mode === 'lax';
    const operator = expr.operator;
    if (operator === '&&' || operator === '||') {
      throw new JsonPathError(
        'JSON_PATH_BOOLEAN_EXPECTED',
        'logical operators are only valid in filter predicates',
        expr.pos,
      );
    }
    const left = this.evalScalarExpr(expr.left, ctx, lax);
    const right = this.evalScalarExpr(expr.right, ctx, lax);
    if (typeof left !== 'number' || typeof right !== 'number') {
      throw new JsonPathError(
        'JSON_PATH_NUMERIC_EXPECTED',
        `arithmetic requires numeric items but got ${jsonTypeName(left)} and ${jsonTypeName(right)}`,
        expr.pos,
      );
    }
    switch (operator) {
      case '+':
        return left + right;
      case '-':
        return left - right;
      case '*':
        return left * right;
      case '/':
        if (right === 0) {
          throw new JsonPathError('JSON_PATH_DIVISION_BY_ZERO', 'division by zero', expr.pos);
        }
        return left / right;
      case '%':
        if (right === 0) {
          throw new JsonPathError('JSON_PATH_DIVISION_BY_ZERO', 'modulo by zero', expr.pos);
        }
        return left - Math.floor(left / right) * right;
      case '==':
      case '!=':
      case '<':
      case '<=':
      case '>':
      case '>=':
        throw new JsonPathError(
          'JSON_PATH_BOOLEAN_EXPECTED',
          'comparisons are only valid in filter predicates',
          expr.pos,
        );
    }
  }

  /**
   * Evaluate a filter predicate under Kleene three-valued logic.
   * Any error raised inside the predicate is caught and mapped to UNKNOWN,
   * regardless of strict/lax mode — the surrounding path keeps evaluating.
   */
  private evalPredicate(expr: Expr, ctx: EvalContext): TriState {
    try {
      return this.evalPredicateUnsafe(expr, ctx);
    } catch (error) {
      if (error instanceof JsonPathError) return 'UNKNOWN';
      throw error;
    }
  }

  private evalPredicateUnsafe(expr: Expr, ctx: EvalContext): TriState {
    switch (expr.type) {
      case 'literal':
        if (typeof expr.value === 'boolean') return expr.value ? 'TRUE' : 'FALSE';
        throw new JsonPathError(
          'JSON_PATH_BOOLEAN_EXPECTED',
          'filter predicate must be boolean',
          expr.pos,
        );
      case 'unary': {
        if (expr.operator !== '!') {
          throw new JsonPathError(
            'JSON_PATH_BOOLEAN_EXPECTED',
            'numeric unary used in a predicate',
            expr.pos,
          );
        }
        // Kleene NOT: NOT(UNKNOWN) stays UNKNOWN.
        const inner = this.evalPredicate(expr.argument, ctx);
        if (inner === 'UNKNOWN') return 'UNKNOWN';
        return inner === 'TRUE' ? 'FALSE' : 'TRUE';
      }
      case 'binary':
        return this.evalBinaryPredicate(expr, ctx);
      case 'exists':
        return this.evalExists(expr, ctx);
      case 'likeRegex':
        return this.evalLikeRegex(expr, ctx);
      case 'path': {
        // Bare paths in predicates follow exists() semantics: a non-empty
        // sequence is TRUE even when its single item is boolean `false`.
        const seq = this.evalPath(expr, ctx);
        return seq.length > 0 ? 'TRUE' : 'FALSE';
      }
      case 'root':
      case 'current':
      case 'variable': {
        const seq = this.evalExprSequence(expr, ctx);
        if (seq.length === 0) return 'FALSE';
        if (seq.length === 1 && typeof seq[0] === 'boolean') {
          return seq[0] ? 'TRUE' : 'FALSE';
        }
        throw new JsonPathError(
          'JSON_PATH_BOOLEAN_EXPECTED',
          'non-boolean predicate expression',
          expr.pos,
        );
      }
    }
  }

  private evalBinaryPredicate(expr: BinaryExpr, ctx: EvalContext): TriState {
    const operator = expr.operator;
    if (operator === '&&') {
      const left = this.evalPredicate(expr.left, ctx);
      if (left === 'FALSE') return 'FALSE';
      const right = this.evalPredicate(expr.right, ctx);
      if (right === 'FALSE') return 'FALSE';
      if (left === 'UNKNOWN' || right === 'UNKNOWN') return 'UNKNOWN';
      return 'TRUE';
    }
    if (operator === '||') {
      const left = this.evalPredicate(expr.left, ctx);
      if (left === 'TRUE') return 'TRUE';
      const right = this.evalPredicate(expr.right, ctx);
      if (right === 'TRUE') return 'TRUE';
      if (left === 'UNKNOWN' || right === 'UNKNOWN') return 'UNKNOWN';
      return 'FALSE';
    }

    const leftSeq = this.evalExprSequence(expr.left, ctx);
    const rightSeq = this.evalExprSequence(expr.right, ctx);

    // Empty sequence on either side: FALSE (never UNKNOWN).
    if (leftSeq.length === 0 || rightSeq.length === 0) return 'FALSE';

    let sawUnknown = false;
    for (const left of leftSeq) {
      for (const right of rightSeq) {
        const result = this.comparePair(operator as ComparisonOperator, left, right);
        if (result === 'TRUE') return 'TRUE';
        if (result === 'UNKNOWN') sawUnknown = true;
      }
    }
    return sawUnknown ? 'UNKNOWN' : 'FALSE';
  }

  private comparePair(
    operator: ComparisonOperator,
    left: JsonValue,
    right: JsonValue,
  ): TriState {
    try {
      switch (operator) {
        case '==':
          return deepEqual(left, right) ? 'TRUE' : 'FALSE';
        case '!=':
          return deepEqual(left, right) ? 'FALSE' : 'TRUE';
        case '<':
        case '<=':
        case '>':
        case '>=': {
          const ordering = compareOrdered(left, right);
          if (ordering === null) return 'UNKNOWN';
          const satisfied =
            (operator === '<' && ordering < 0) ||
            (operator === '<=' && ordering <= 0) ||
            (operator === '>' && ordering > 0) ||
            (operator === '>=' && ordering >= 0);
          return satisfied ? 'TRUE' : 'FALSE';
        }
      }
    } catch (error) {
      if (error instanceof JsonPathError) return 'UNKNOWN';
      throw error;
    }
  }

  private evalExists(expr: Extract<Expr, { type: 'exists' }>, ctx: EvalContext): TriState {
    try {
      const seq = this.evalPath(expr.argument, ctx);
      return seq.length > 0 ? 'TRUE' : 'FALSE';
    } catch (error) {
      if (error instanceof JsonPathError) {
        // A structural error means the path cannot exist. Non-structural errors
        // still surface here; the enclosing predicate wrapper turns them into UNKNOWN.
        if (isStructuralError(error.code)) return 'FALSE';
        throw error;
      }
      throw error;
    }
  }

  private evalLikeRegex(expr: Extract<Expr, { type: 'likeRegex' }>, ctx: EvalContext): TriState {
    const seq = this.evalExprSequence(expr.left, ctx);
    if (seq.length === 0) return 'FALSE';
    let sawUnknown = false;
    for (const item of seq) {
      if (typeof item !== 'string') {
        sawUnknown = true;
        continue;
      }
      const regex = compileLikeRegex(expr.pattern, expr.flags, expr.pos);
      if (regex.test(item)) return 'TRUE';
    }
    return sawUnknown ? 'UNKNOWN' : 'FALSE';
  }
}

/** Ordering only exists between two numbers or two strings; null/booleans/objects yield UNKNOWN. */
function compareOrdered(left: JsonValue, right: JsonValue): number | null {
  if (typeof left === 'number' && typeof right === 'number') {
    return left < right ? -1 : left > right ? 1 : 0;
  }
  if (typeof left === 'string' && typeof right === 'string') {
    return left < right ? -1 : left > right ? 1 : 0;
  }
  return null;
}

/**
 * Translate XQuery-style like_regex flags onto JavaScript RegExp flags.
 * `i` case-insensitive, `s` dot matches newline, `m` multiline, `x` extended
 * (un-escaped whitespace in the pattern is ignored).
 */
function compileLikeRegex(pattern: string, flags: string, pos: number): RegExp {
  let jsFlags = '';
  let extended = false;
  for (const flag of flags) {
    if (flag === 'i' || flag === 's' || flag === 'm') {
      if (!jsFlags.includes(flag)) jsFlags += flag;
    } else if (flag === 'x') {
      extended = true;
    } else {
      throw new JsonPathError(
        'JSON_PATH_INVALID_REGEX',
        `unsupported like_regex flag ${JSON.stringify(flag)}`,
        pos,
      );
    }
  }
  let source = pattern;
  if (extended) {
    source = stripExtendedWhitespace(pattern);
  }
  try {
    return new RegExp(source, jsFlags);
  } catch (error) {
    throw new JsonPathError(
      'JSON_PATH_INVALID_REGEX',
      `invalid like_regex pattern: ${(error as Error).message}`,
      pos,
    );
  }
}

function stripExtendedWhitespace(pattern: string): string {
  let out = '';
  let inClass = false;
  for (let i = 0; i < pattern.length; i += 1) {
    const ch = pattern[i];
    if (ch === '\\') {
      out += ch + (pattern[i + 1] ?? '');
      i += 1;
      continue;
    }
    if (ch === '[') inClass = true;
    if (ch === ']') inClass = false;
    if (!inClass && /\s/.test(ch)) continue;
    out += ch;
  }
  return out;
}

export function queryJsonPath(
  document: JsonValue,
  path: string | JsonPathAst,
  options: EvalOptions = {},
): JsonValue[] {
  const ast = typeof path === 'string' ? parseAst(path) : path;
  return new JsonPathEvaluator(ast).query(document, options);
}

export function matchJsonPath(
  document: JsonValue,
  path: string | JsonPathAst,
  options: EvalOptions = {},
): boolean {
  const ast = typeof path === 'string' ? parseAst(path) : path;
  return new JsonPathEvaluator(ast).match(document, options);
}
