export type {
  ArrayIndexSelector,
  BooleanExpression,
  ContainmentQuery,
  EvaluateOptions,
  EvaluationFailure,
  EvaluationResult,
  EvaluationSuccess,
  ExistsExpression,
  Expression,
  IndexExpression,
  IndexExtractionOptions,
  IndexPathSegment,
  IndexPlan,
  IndexTerm,
  JsonPath,
  JsonPathErrorCode,
  JsonPathEvaluationErrorLike,
  JsonPathMode,
  JsonPrimitive,
  JsonValue,
  LikeRegexExpression,
  LiteralExpression,
  PathExpression,
  PathExpressionOrigin,
  PathInput,
  PathStep,
  ParseOptions,
  TruthValue,
} from "./types.js";

export {
  JsonPathEvaluationError,
  JsonPathParseError,
  isJsonPathError,
} from "./errors.js";
export { parseJsonPath, JsonPathParser } from "./parser.js";
export {
  evaluateJsonPath,
  tryEvaluateJsonPath,
  jsonEqual,
  jsonType,
  isJsonArray,
  isJsonObject,
  truthValueToJson,
} from "./evaluator.js";
export {
  extractIndexPlan,
  extractJsonPathIndexPlan,
  extractIndexTerms,
  indexPlan,
  indexPlanMatchesDocument,
  matchesIndexPlan,
  matchesPlan,
  toContainmentQuery,
  toJsonbContainment,
} from "./index-plan.js";

import { evaluateJsonPath, tryEvaluateJsonPath } from "./evaluator.js";
import { extractIndexPlan } from "./index-plan.js";
import { parseJsonPath } from "./parser.js";
import type {
  EvaluateOptions,
  EvaluationResult,
  IndexPlan,
  JsonPathMode,
  JsonValue,
  PathInput,
} from "./types.js";

export const parse = parseJsonPath;
export const compile = parseJsonPath;
export const evaluate = evaluateJsonPath;
export const query = evaluateJsonPath;
export const jsonPathQuery = evaluateJsonPath;

export function evaluateJsonPathResult(
  document: JsonValue,
  path: PathInput,
  options: EvaluateOptions = {},
): EvaluationResult {
  return tryEvaluateJsonPath(path, document, options);
}

export function jsonPathExists(
  path: string,
  document: JsonValue,
  options: EvaluateOptions = {},
): boolean {
  return evaluateJsonPath(path, document, options).length > 0;
}

export function jsonbPathQuery(
  document: JsonValue,
  path: string,
  options: EvaluateOptions = {},
): JsonValue[] {
  return evaluateJsonPath(path, document, options);
}

export function jsonbPathExists(
  document: JsonValue,
  path: string,
  options: EvaluateOptions = {},
): boolean {
  return jsonPathExists(path, document, options);
}

export function evaluateJsonPathSafe(
  document: JsonValue,
  path: PathInput,
  options: EvaluateOptions = {},
): EvaluationResult {
  return tryEvaluateJsonPath(path, document, options);
}

export function indexPlanFor(
  path: PathInput,
  mode: JsonPathMode = "lax",
): IndexPlan {
  return extractIndexPlan(path, { mode });
}
