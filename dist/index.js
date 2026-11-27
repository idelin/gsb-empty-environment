export { JsonPathEvaluationError, JsonPathParseError, isJsonPathError, } from "./errors.js";
export { parseJsonPath, JsonPathParser } from "./parser.js";
export { evaluateJsonPath, tryEvaluateJsonPath, jsonEqual, jsonType, isJsonArray, isJsonObject, truthValueToJson, } from "./evaluator.js";
export { extractIndexPlan, extractJsonPathIndexPlan, extractIndexTerms, indexPlan, indexPlanMatchesDocument, matchesIndexPlan, matchesPlan, toContainmentQuery, toJsonbContainment, } from "./index-plan.js";
import { evaluateJsonPath, tryEvaluateJsonPath } from "./evaluator.js";
import { extractIndexPlan } from "./index-plan.js";
import { parseJsonPath } from "./parser.js";
export const parse = parseJsonPath;
export const compile = parseJsonPath;
export const evaluate = evaluateJsonPath;
export const query = evaluateJsonPath;
export const jsonPathQuery = evaluateJsonPath;
export function evaluateJsonPathResult(document, path, options = {}) {
    return tryEvaluateJsonPath(path, document, options);
}
export function jsonPathExists(path, document, options = {}) {
    return evaluateJsonPath(path, document, options).length > 0;
}
export function jsonbPathQuery(document, path, options = {}) {
    return evaluateJsonPath(path, document, options);
}
export function jsonbPathExists(document, path, options = {}) {
    return jsonPathExists(path, document, options);
}
export function evaluateJsonPathSafe(document, path, options = {}) {
    return tryEvaluateJsonPath(path, document, options);
}
export function indexPlanFor(path, mode = "lax") {
    return extractIndexPlan(path, { mode });
}
//# sourceMappingURL=index.js.map