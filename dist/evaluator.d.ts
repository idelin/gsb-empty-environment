import type { EvaluateOptions, EvaluationResult, JsonPath, JsonPathMode, JsonValue, PathExpression, TruthValue } from "./types.js";
interface EvaluationContext {
    readonly mode: JsonPathMode;
    readonly root: JsonValue;
    readonly current: JsonValue;
    readonly last?: number;
    readonly maxDepth: number;
    readonly depth: number;
}
export declare function isJsonObject(value: JsonValue): value is {
    [key: string]: JsonValue;
};
export declare function isJsonArray(value: JsonValue): value is JsonValue[];
export declare function jsonType(value: JsonValue): "null" | "boolean" | "number" | "string" | "array" | "object";
export declare function jsonEqual(left: JsonValue, right: JsonValue): boolean;
export declare function evaluateJsonPath(path: string | JsonPath, document: JsonValue, options?: EvaluateOptions): JsonValue[];
export declare function tryEvaluateJsonPath(path: string | JsonPath, document: JsonValue, options?: EvaluateOptions): EvaluationResult;
export declare function evaluatePathExpression(path: PathExpression, context: EvaluationContext): JsonValue[];
export declare function truthValueToJson(value: TruthValue): JsonValue;
export {};
//# sourceMappingURL=evaluator.d.ts.map