import { JsonPathEvaluationError, isJsonPathError } from "./errors.js";
import { parseJsonPath } from "./parser.js";
import type {
  ArrayIndexSelector,
  BooleanExpression,
  EvaluateOptions,
  EvaluationResult,
  Expression,
  IndexExpression,
  JsonPath,
  JsonPathEvaluationErrorLike,
  JsonPathMode,
  JsonValue,
  MethodStep,
  PathExpression,
  PathStep,
  TruthValue,
} from "./types.js";

interface EvaluationContext {
  readonly mode: JsonPathMode;
  readonly root: JsonValue;
  readonly current: JsonValue;
  readonly last?: number;
  readonly maxDepth: number;
  readonly depth: number;
}

export function isJsonObject(
  value: JsonValue,
): value is { [key: string]: JsonValue } {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function isJsonArray(value: JsonValue): value is JsonValue[] {
  return Array.isArray(value);
}

export function jsonType(
  value: JsonValue,
):
  | "null"
  | "boolean"
  | "number"
  | "string"
  | "array"
  | "object" {
  if (value === null) {
    return "null";
  }
  if (Array.isArray(value)) {
    return "array";
  }
  if (typeof value === "object") {
    return "object";
  }
  if (typeof value === "number") {
    return "number";
  }
  if (typeof value === "string") {
    return "string";
  }
  return "boolean";
}

export function jsonEqual(left: JsonValue, right: JsonValue): boolean {
  if (left === right) {
    return true;
  }
  if (
    left === null ||
    right === null ||
    typeof left !== "object" ||
    typeof right !== "object"
  ) {
    return false;
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right)) {
      return false;
    }
    if (left.length !== right.length) {
      return false;
    }
    return left.every((value, index) => jsonEqual(value, right[index]!));
  }
  const leftKeys = Object.keys(left);
  const rightKeys = Object.keys(right);
  if (leftKeys.length !== rightKeys.length) {
    return false;
  }
  return leftKeys.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(right, key) &&
      jsonEqual(left[key]!, right[key]!),
  );
}

function strictError(
  context: EvaluationContext,
  code:
    | "MEMBER_ON_NON_OBJECT"
    | "MISSING_KEY"
    | "INDEX_ON_NON_ARRAY"
    | "INDEX_OUT_OF_BOUNDS"
    | "WILDCARD_ON_SCALAR"
    | "METHOD_ON_INVALID_TYPE"
    | "TYPE_ERROR"
    | "ARITHMETIC_ERROR"
    | "INVALID_REGEX"
    | "DEPTH_LIMIT",
  message: string,
  offset?: number,
): never {
  throw new JsonPathEvaluationError(code, message, {
    mode: context.mode,
    offset,
  });
}

export function evaluateJsonPath(
  path: string | JsonPath,
  document: JsonValue,
  options: EvaluateOptions = {},
): JsonValue[] {
  const ast =
    typeof path === "string"
      ? parseJsonPath(path, { defaultMode: options.defaultMode })
      : path;
  const context: EvaluationContext = {
    mode: ast.mode,
    root: document,
    current: document,
    maxDepth: options.maxDepth ?? 1_000,
    depth: 0,
  };
  return evaluatePathExpression(ast.root, context);
}

export function tryEvaluateJsonPath(
  path: string | JsonPath,
  document: JsonValue,
  options: EvaluateOptions = {},
): EvaluationResult {
  let mode: JsonPathMode = options.defaultMode ?? "lax";
  try {
    const ast =
      typeof path === "string"
        ? parseJsonPath(path, { defaultMode: options.defaultMode })
        : path;
    mode = ast.mode;
    const values = evaluateJsonPath(ast, document, options);
    return { ok: true, mode, values };
  } catch (error) {
    const normalized =
      error instanceof Error
        ? (error as Error & {
            code?: string;
            mode?: JsonPathMode;
            offset?: number;
          })
        : undefined;
    if (normalized === undefined) {
      const wrapped = new JsonPathEvaluationError(
        "TYPE_ERROR",
        String(error),
        { mode },
      );
      return { ok: false, mode, values: [], error: wrapped };
    }
    if (normalized.code === undefined) {
      return {
        ok: false,
        mode,
        values: [],
        error: new JsonPathEvaluationError("TYPE_ERROR", normalized.message, {
          mode,
          cause: normalized,
        }),
      };
    }
    return {
      ok: false,
      mode: normalized.mode ?? mode,
      values: [],
      error: normalized as JsonPathEvaluationErrorLike,
    };
  }
}

export function evaluatePathExpression(
  path: PathExpression,
  context: EvaluationContext,
): JsonValue[] {
  const initial =
    path.origin === "$" ? [context.root] : [context.current];
  return evaluateSteps(initial, path.steps, context);
}

function evaluateSteps(
  initial: JsonValue[],
  steps: readonly PathStep[],
  context: EvaluationContext,
): JsonValue[] {
  let values = initial;

  for (const step of steps) {
    values = applyStep(values, step, context);
  }

  return values;
}

function applyStep(
  values: JsonValue[],
  step: PathStep,
  context: EvaluationContext,
): JsonValue[] {
  switch (step.kind) {
    case "member":
      return applyMemberStep(values, step.key, step.offset, context);
    case "memberWildcard":
      return applyMemberWildcard(values, step.offset, context);
    case "arrayAccess":
      return applyArrayAccess(values, step, context);
    case "recursive":
      return applyRecursive(values, context);
    case "method":
      return applyMethodStep(values, step, context);
    case "filter":
      return applyFilter(values, step.predicate, context);
  }
}

function applyMemberStep(
  values: JsonValue[],
  key: string,
  offset: number,
  context: EvaluationContext,
): JsonValue[] {
  const output: JsonValue[] = [];

  for (const value of values) {
    if (context.mode === "strict") {
      if (!isJsonObject(value)) {
        strictError(
          context,
          "MEMBER_ON_NON_OBJECT",
          `member accessor ".${key}" can only be applied to an object`,
          offset,
        );
      }
      if (!Object.prototype.hasOwnProperty.call(value, key)) {
        strictError(
          context,
          "MISSING_KEY",
          `object does not contain key "${key}"`,
          offset,
        );
      }
      output.push(value[key]!);
      continue;
    }
    collectLaxMember(value, key, output, context, context.depth);
  }

  return output;
}

function collectLaxMember(
  value: JsonValue,
  key: string,
  output: JsonValue[],
  context: EvaluationContext,
  depth: number,
): void {
  if (depth > context.maxDepth) {
    strictError(context, "DEPTH_LIMIT", "jsonpath evaluation depth limit exceeded");
  }
  if (Array.isArray(value)) {
    for (const element of value) {
      collectLaxMember(element, key, output, context, depth + 1);
    }
    return;
  }
  if (isJsonObject(value) && Object.prototype.hasOwnProperty.call(value, key)) {
    output.push(value[key]!);
  }
}

function applyMemberWildcard(
  values: JsonValue[],
  offset: number,
  context: EvaluationContext,
): JsonValue[] {
  const output: JsonValue[] = [];

  for (const value of values) {
    if (context.mode === "strict") {
      if (!isJsonObject(value)) {
        strictError(
          context,
          "WILDCARD_ON_SCALAR",
          "member wildcard can only be applied to an object",
          offset,
        );
      }
      output.push(...Object.values(value));
      continue;
    }
    collectLaxMemberWildcard(value, output, context, context.depth);
  }

  return output;
}

function collectLaxMemberWildcard(
  value: JsonValue,
  output: JsonValue[],
  context: EvaluationContext,
  depth: number,
): void {
  if (depth > context.maxDepth) {
    strictError(context, "DEPTH_LIMIT", "jsonpath evaluation depth limit exceeded");
  }
  if (Array.isArray(value)) {
    for (const element of value) {
      collectLaxMemberWildcard(element, output, context, depth + 1);
    }
    return;
  }
  if (isJsonObject(value)) {
    output.push(...Object.values(value));
  }
}

function applyArrayAccess(
  values: JsonValue[],
  step: Extract<PathStep, { kind: "arrayAccess" }>,
  context: EvaluationContext,
): JsonValue[] {
  const output: JsonValue[] = [];

  for (const value of values) {
    const array =
      Array.isArray(value) || context.mode === "strict" ? value : [value];
    if (!Array.isArray(array)) {
      strictError(
        context,
        "INDEX_ON_NON_ARRAY",
        "array accessor can only be applied to an array",
        step.offset,
      );
    }

    if (step.wildcard) {
      output.push(...array);
      continue;
    }

    const selected = new Set<number>();
    for (const selector of step.selectors) {
      if (selector.kind === "index") {
        let index: number;
        try {
          index = evaluateIndexExpression(selector.expression, array.length);
        } catch (error) {
          if (context.mode === "lax" && isJsonPathError(error)) {
            continue;
          }
          throw error;
        }
        if (index < 0 || index >= array.length) {
          if (context.mode === "strict") {
            strictError(
              context,
              "INDEX_OUT_OF_BOUNDS",
              `array index ${index} is out of bounds`,
              step.offset,
            );
          }
          continue;
        }
        selected.add(index);
        continue;
      }

      let from: number;
      let to: number;
      try {
        from = evaluateIndexExpression(selector.from, array.length);
        to = evaluateIndexExpression(selector.to, array.length);
      } catch (error) {
        if (context.mode === "lax" && isJsonPathError(error)) {
          continue;
        }
        throw error;
      }

      if (from > to) {
        continue;
      }
      for (let index = from; index <= to; index += 1) {
        if (index < 0 || index >= array.length) {
          if (context.mode === "strict") {
            strictError(
              context,
              "INDEX_OUT_OF_BOUNDS",
              `array index ${index} is out of bounds`,
              step.offset,
            );
          }
          continue;
        }
        selected.add(index);
      }
    }

    for (const index of selected) {
      output.push(array[index]!);
    }
  }

  return output;
}

function evaluateIndexExpression(
  expression: IndexExpression,
  length: number,
): number {
  let value: number;

  switch (expression.kind) {
    case "integer":
      value = expression.value;
      break;
    case "last":
      value = length - 1;
      break;
    case "unary": {
      const operand = evaluateIndexExpression(expression.operand, length);
      value = expression.operator === "+" ? operand : -operand;
      break;
    }
    case "binary": {
      const left = evaluateIndexExpression(expression.left, length);
      const right = evaluateIndexExpression(expression.right, length);
      switch (expression.operator) {
        case "+":
          value = left + right;
          break;
        case "-":
          value = left - right;
          break;
        case "*":
          value = left * right;
          break;
        case "/":
          if (right === 0) {
            throw new JsonPathEvaluationError(
              "ARITHMETIC_ERROR",
              "division by zero in array index",
            );
          }
          value = left / right;
          break;
        case "%":
          if (right === 0) {
            throw new JsonPathEvaluationError(
              "ARITHMETIC_ERROR",
              "division by zero in array index",
            );
          }
          value = left % right;
          break;
      }
      break;
    }
  }

  if (!Number.isInteger(value)) {
    throw new JsonPathEvaluationError(
      "TYPE_ERROR",
      "array index expression did not produce an integer",
    );
  }
  return value;
}

function applyRecursive(
  values: JsonValue[],
  context: EvaluationContext,
): JsonValue[] {
  const output: JsonValue[] = [];
  for (const value of values) {
    collectRecursive(value, output, context, context.depth);
  }
  return output;
}

function collectRecursive(
  value: JsonValue,
  output: JsonValue[],
  context: EvaluationContext,
  depth: number,
): void {
  if (depth > context.maxDepth) {
    strictError(context, "DEPTH_LIMIT", "jsonpath evaluation depth limit exceeded");
  }
  output.push(value);
  if (Array.isArray(value)) {
    for (const element of value) {
      collectRecursive(element, output, context, depth + 1);
    }
  } else if (isJsonObject(value)) {
    for (const child of Object.values(value)) {
      collectRecursive(child, output, context, depth + 1);
    }
  }
}

function applyMethodStep(
  values: JsonValue[],
  step: MethodStep,
  context: EvaluationContext,
): JsonValue[] {
  const output: JsonValue[] = [];

  for (const value of values) {
    try {
      if (
        context.mode === "lax" &&
        step.name !== "type" &&
        step.name !== "size" &&
        Array.isArray(value)
      ) {
        for (const element of value) {
          output.push(...applyMethod(step, element));
        }
        continue;
      }
      if (
        context.mode === "lax" &&
        step.name === "size" &&
        !Array.isArray(value)
      ) {
        output.push(1);
        continue;
      }
      output.push(...applyMethod(step, value));
    } catch (error) {
      if (context.mode === "lax" && isJsonPathError(error)) {
        continue;
      }
      throw error;
    }
  }

  return output;
}

function applyMethod(step: MethodStep, value: JsonValue): JsonValue[] {
  if (step.args.length > 0) {
    throw new JsonPathEvaluationError(
      "METHOD_ON_INVALID_TYPE",
      `method "${step.name}" does not accept arguments`,
      { offset: step.offset },
    );
  }

  switch (step.name) {
    case "type":
      return [jsonType(value)];
    case "size":
      if (!Array.isArray(value)) {
        throw new JsonPathEvaluationError(
          "METHOD_ON_INVALID_TYPE",
          "size() requires an array in strict mode",
          { offset: step.offset },
        );
      }
      return [value.length];
    case "double":
      if (typeof value === "number") {
        return [value];
      }
      if (typeof value === "string" && value.trim() !== "") {
        const number = Number(value);
        if (Number.isFinite(number)) {
          return [number];
        }
      }
      throw new JsonPathEvaluationError(
        "METHOD_ON_INVALID_TYPE",
        "double() requires a number or numeric string",
        { offset: step.offset },
      );
    case "abs":
    case "floor":
    case "ceiling": {
      if (typeof value !== "number") {
        throw new JsonPathEvaluationError(
          "METHOD_ON_INVALID_TYPE",
          `${step.name}() requires a number`,
          { offset: step.offset },
        );
      }
      if (step.name === "abs") {
        return [Math.abs(value)];
      }
      if (step.name === "floor") {
        return [Math.floor(value)];
      }
      return [Math.ceil(value)];
    }
    default:
      throw new JsonPathEvaluationError(
        "METHOD_ON_INVALID_TYPE",
        `unsupported method "${step.name}"`,
        { offset: step.offset },
      );
  }
}

function applyFilter(
  values: JsonValue[],
  predicate: BooleanExpression,
  context: EvaluationContext,
): JsonValue[] {
  const output: JsonValue[] = [];

  const candidates =
    context.mode === "lax"
      ? values.flatMap((value) => (Array.isArray(value) ? value : [value]))
      : values;

  for (const value of candidates) {
    const result = safeEvaluateTruth(predicate, {
      ...context,
      current: value,
    });
    if (result === true) {
      output.push(value);
    }
  }

  return output;
}

function safeEvaluateTruth(
  expression: BooleanExpression,
  context: EvaluationContext,
): TruthValue {
  try {
    return evaluateTruth(expression, context);
  } catch (error) {
    if (isJsonPathError(error) && error.code !== "DEPTH_LIMIT") {
      return "unknown";
    }
    throw error;
  }
}

function evaluateExpression(
  expression: Expression,
  context: EvaluationContext,
): JsonValue[] {
  switch (expression.kind) {
    case "literal":
      return [expression.value];
    case "path":
      return evaluatePathExpression(expression, context);
    case "exists":
      return [
        evaluatePathExpression(expression.path, context).length > 0,
      ];
    case "last":
      if (context.last === undefined) {
        strictError(
          context,
          "TYPE_ERROR",
          "'last' is only valid in an array index expression",
          expression.offset,
        );
      }
      return [context.last];
    case "unary":
      return evaluateUnary(expression.operator, expression.operand, context);
    case "binary":
      switch (expression.operator) {
        case "+":
        case "-":
        case "*":
        case "/":
        case "%":
          return evaluateArithmetic(
            expression.operator,
            expression.left,
            expression.right,
            context,
          );
        case "&&":
        case "||": {
          const value = evaluateLogical(
            expression.operator,
            expression.left,
            expression.right,
            context,
          );
          return value === "unknown" ? [] : [value];
        }
        case "==":
        case "!=":
        case "<":
        case "<=":
        case ">":
        case ">=": {
          const value = evaluateComparison(
            expression.operator,
            expression.left,
            expression.right,
            context,
          );
          return value === "unknown" ? [] : [value];
        }
      }
    // fall through
    case "likeRegex": {
      const value = evaluateLikeRegex(expression, context);
      return value === "unknown" ? [] : [value];
    }
  }
}

function evaluateTruth(
  expression: BooleanExpression,
  context: EvaluationContext,
): TruthValue {
  switch (expression.kind) {
    case "path":
      return evaluatePathExpression(expression, context).length > 0;
    case "exists":
      return evaluatePathExpression(expression.path, context).length > 0;
    case "literal":
      if (expression.value === true || expression.value === false) {
        return expression.value;
      }
      return expression.value === null ? "unknown" : "unknown";
    case "unary":
  if (expression.operator === "!") {
    return negateTruth(safeEvaluateTruth(expression.operand, context));
  }
      return "unknown";
    case "binary":
      switch (expression.operator) {
        case "&&":
        case "||":
          return evaluateLogical(
            expression.operator,
            expression.left,
            expression.right,
            context,
          );
        case "==":
        case "!=":
        case "<":
        case "<=":
        case ">":
        case ">=":
          return evaluateComparison(
            expression.operator,
            expression.left,
            expression.right,
            context,
          );
        default:
          return "unknown";
      }
    case "likeRegex":
      return evaluateLikeRegex(expression, context);
    case "last":
      return "unknown";
  }
}

function negateTruth(value: TruthValue): TruthValue {
  if (value === "unknown") {
    return "unknown";
  }
  return !value;
}

function evaluateLogical(
  operator: "&&" | "||",
  left: Expression,
  right: Expression,
  context: EvaluationContext,
): TruthValue {
  const leftValue = safeEvaluateTruth(left, context);

  if (operator === "&&") {
    if (leftValue === false) {
      return false;
    }
    const rightValue = safeEvaluateTruth(right, context);
    if (rightValue === false) {
      return false;
    }
    if (leftValue === "unknown" || rightValue === "unknown") {
      return "unknown";
    }
    return true;
  }

  if (leftValue === true) {
    return true;
  }
  const rightValue = safeEvaluateTruth(right, context);
  if (rightValue === true) {
    return true;
  }
  if (leftValue === "unknown" || rightValue === "unknown") {
    return "unknown";
  }
  return false;
}

function evaluateUnary(
  operator: "+" | "-" | "!",
  operand: Expression,
  context: EvaluationContext,
): JsonValue[] {
  if (operator === "!") {
    const value = negateTruth(evaluateTruth(operand, context));
    return value === "unknown" ? [] : [value];
  }

  const values = evaluateExpression(operand, context);
  return values.map((value) => {
    const number = toNumber(value);
    return operator === "+" ? number : -number;
  });
}

function evaluateArithmetic(
  operator: "+" | "-" | "*" | "/" | "%",
  leftExpression: Expression,
  rightExpression: Expression,
  context: EvaluationContext,
): JsonValue[] {
  const leftValues = evaluateExpression(leftExpression, context);
  const rightValues = evaluateExpression(rightExpression, context);
  const output: JsonValue[] = [];

  for (const leftValue of leftValues) {
    const left = toNumber(leftValue);
    for (const rightValue of rightValues) {
      const right = toNumber(rightValue);
      let value: number;
      switch (operator) {
        case "+":
          value = left + right;
          break;
        case "-":
          value = left - right;
          break;
        case "*":
          value = left * right;
          break;
        case "/":
          if (right === 0) {
            throw new JsonPathEvaluationError(
              "ARITHMETIC_ERROR",
              "division by zero",
            );
          }
          value = left / right;
          break;
        case "%":
          if (right === 0) {
            throw new JsonPathEvaluationError(
              "ARITHMETIC_ERROR",
              "division by zero",
            );
          }
          value = left % right;
          break;
      }
      if (!Number.isFinite(value)) {
        throw new JsonPathEvaluationError(
          "ARITHMETIC_ERROR",
          "arithmetic result is not a finite number",
        );
      }
      output.push(value);
    }
  }

  return output;
}

function toNumber(value: JsonValue): number {
  if (typeof value === "number") {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const converted = Number(value);
    if (Number.isFinite(converted)) {
      return converted;
    }
  }
  throw new JsonPathEvaluationError(
    "TYPE_ERROR",
    `expected a number but found ${jsonType(value)}`,
  );
}

function evaluateComparison(
  operator: "==" | "!=" | "<" | "<=" | ">" | ">=",
  leftExpression: Expression,
  rightExpression: Expression,
  context: EvaluationContext,
): TruthValue {
  const leftValues = evaluateExpression(leftExpression, context);
  const rightValues = evaluateExpression(rightExpression, context);
  const comparisonLeft =
    context.mode === "lax" ? unwrapComparisonOperand(leftValues) : leftValues;
  const comparisonRight =
    context.mode === "lax" ? unwrapComparisonOperand(rightValues) : rightValues;

  if (comparisonLeft.length === 0 || comparisonRight.length === 0) {
    return false;
  }

  let sawUnknown = false;
  for (const left of comparisonLeft) {
    for (const right of comparisonRight) {
      try {
        if (comparePair(operator, left, right)) {
          return true;
        }
      } catch (error) {
        if (isJsonPathError(error)) {
          sawUnknown = true;
        } else {
          throw error;
        }
      }
    }
  }
  return sawUnknown ? "unknown" : false;
}

function unwrapComparisonOperand(values: readonly JsonValue[]): JsonValue[] {
  const output: JsonValue[] = [];
  for (const value of values) {
    if (Array.isArray(value)) {
      output.push(...value);
    } else {
      output.push(value);
    }
  }
  return output;
}

function comparePair(
  operator: "==" | "!=" | "<" | "<=" | ">" | ">=",
  left: JsonValue,
  right: JsonValue,
): boolean {
  const leftType = jsonType(left);
  const rightType = jsonType(right);
  if (leftType !== rightType) {
    throw new JsonPathEvaluationError(
      "TYPE_ERROR",
      `cannot compare ${leftType} and ${rightType}`,
    );
  }
  if (operator === "==") {
    return jsonEqual(left, right);
  }
  if (operator === "!=") {
    return !jsonEqual(left, right);
  }

  const comparison = compareOrdered(left, right);
  switch (operator) {
    case "<":
      return comparison < 0;
    case "<=":
      return comparison <= 0;
    case ">":
      return comparison > 0;
    case ">=":
      return comparison >= 0;
  }
}

function compareOrdered(left: JsonValue, right: JsonValue): number {
  const leftType = jsonType(left);
  const rightType = jsonType(right);
  if (leftType !== rightType) {
    throw new JsonPathEvaluationError(
      "TYPE_ERROR",
      `cannot order ${leftType} and ${rightType}`,
    );
  }
  if (leftType === "null") {
    return 0;
  }
  if (leftType === "number") {
    if (left === right) {
      return 0;
    }
    return (left as number) < (right as number) ? -1 : 1;
  }
  if (leftType === "string") {
    if (left === right) {
      return 0;
    }
    return (left as string) < (right as string) ? -1 : 1;
  }
  if (leftType === "boolean") {
    if (left === right) {
      return 0;
    }
    return left === false ? -1 : 1;
  }
  throw new JsonPathEvaluationError(
    "TYPE_ERROR",
    `cannot order ${leftType} values`,
  );
}

function evaluateLikeRegex(
  expression: Extract<Expression, { kind: "likeRegex" }>,
  context: EvaluationContext,
): TruthValue {
  const values = evaluateExpression(expression.left, context);
  if (values.length === 0) {
    return false;
  }

  const regex = compileLikeRegex(expression.pattern, expression.flags);
  let sawUnknown = false;
  for (const value of values) {
    if (typeof value !== "string") {
      sawUnknown = true;
      continue;
    }
    regex.lastIndex = 0;
    if (regex.test(value)) {
      return true;
    }
  }
  return sawUnknown ? "unknown" : false;
}

function compileLikeRegex(pattern: string, flags: string): RegExp {
  let source = pattern;
  let jsFlags = "";

  for (const flag of flags) {
    switch (flag) {
      case "i":
      case "m":
      case "s":
        if (!jsFlags.includes(flag)) {
          jsFlags += flag;
        }
        break;
      case "q":
        source = source.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
        break;
      case "x":
        source = source
          .replace(/\\#/gu, "#")
          .replace(/#[^\n]*/gu, "")
          .replace(/\s+/gu, "");
        break;
      case "g":
      case "y":
      case "u":
        break;
      default:
        throw new JsonPathEvaluationError(
          "INVALID_REGEX",
          `unsupported like_regex flag "${flag}"`,
        );
    }
  }

  try {
    return new RegExp(source, `${jsFlags}u`);
  } catch (error) {
    throw new JsonPathEvaluationError(
      "INVALID_REGEX",
      `invalid like_regex pattern: ${(error as Error).message}`,
      { cause: error },
    );
  }
}

export function truthValueToJson(value: TruthValue): JsonValue {
  return value === "unknown" ? null : value;
}
