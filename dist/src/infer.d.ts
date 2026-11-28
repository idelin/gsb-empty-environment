import type { Expr } from "./ast.js";
import type { EnvInput, Environment, Type, TypeScheme } from "./types.js";
export interface InferOptions {
    readonly includeBuiltins?: boolean;
}
export interface InferenceResult {
    readonly type: Type;
    readonly scheme: TypeScheme;
    readonly typeString: string;
    readonly schemeString: string;
    readonly principalType: string;
}
/**
 * The prelude used by the source parser.  All list and ref operations are
 * ordinary HM constants, so their types are instantiated at every use.
 */
export declare function builtinEnvironment(): Environment;
export declare const defaultEnvironment: typeof builtinEnvironment;
export declare const builtinEnv: typeof builtinEnvironment;
export declare const initialEnvironment: typeof builtinEnvironment;
export declare function emptyEnvironment(): Environment;
export declare const emptyEnv: typeof emptyEnvironment;
export declare function normalizeEnvironment(input?: EnvInput): Environment;
declare function extendEnvironment(environment: Environment, name: string, scheme: TypeScheme): Environment;
export declare const extendEnv: typeof extendEnvironment;
/**
 * The syntactic non-expansive (value) restriction.  Applications (including
 * `ref e`), dereferences, assignments, projections, lets and conditionals are
 * deliberately excluded.  Record/tuple constructors are values exactly when
 * their components are values.
 */
export declare function isNonExpansive(expression: Expr): boolean;
export declare const isValueExpression: typeof isNonExpansive;
export declare const isSyntacticValue: typeof isNonExpansive;
export declare const isValue: typeof isNonExpansive;
export declare const isNonExpansiveValue: typeof isNonExpansive;
/**
 * Infer the (monomorphic) type of an expression.  Use `inferScheme` for the
 * canonical principal scheme of a closed expression.
 */
export declare function infer(input: Expr | string, environmentInput?: EnvInput, options?: InferOptions): Type;
export declare const inferType: typeof infer;
export declare const inferExpressionType: typeof infer;
export declare const inferExpression: typeof infer;
export declare const inferAST: typeof infer;
export declare const inferSource: typeof infer;
export declare const inferProgram: typeof infer;
export declare const inferTypeOf: typeof infer;
export declare function inferScheme(input: Expr | string, environmentInput?: EnvInput, options?: InferOptions): TypeScheme;
export declare const principalScheme: typeof inferScheme;
export declare const inferPrincipalScheme: typeof inferScheme;
export declare function inferFull(input: Expr | string, environmentInput?: EnvInput, options?: InferOptions): InferenceResult;
export declare const analyze: typeof inferFull;
export declare const inferResult: typeof inferFull;
export declare const analyzeExpression: typeof inferFull;
export declare function principalType(input: Expr | string, environmentInput?: EnvInput, options?: InferOptions): string;
export declare const inferTypeString: typeof principalType;
export declare const typeOf: typeof principalType;
export declare const principalTypeText: typeof principalType;
export declare function check(input: Expr | string, environmentInput?: EnvInput, options?: InferOptions): InferenceResult;
export declare const typecheck: typeof check;
export declare const typeCheck: typeof check;
export declare function formatInferredType(input: Expr | string, environmentInput?: EnvInput, options?: InferOptions): string;
export declare const inferAndFormat: typeof formatInferredType;
export declare class Inferencer {
    private environment;
    private readonly options;
    constructor(environmentInput?: EnvInput, options?: InferOptions);
    infer(input: Expr | string): Type;
    inferScheme(input: Expr | string): TypeScheme;
    bind(name: string, value: Type | TypeScheme): this;
    environmentSnapshot(): Environment;
}
export declare const Infer: typeof Inferencer;
export {};
