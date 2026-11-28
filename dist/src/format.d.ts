import type { Type, TypeScheme } from "./types.js";
export interface FormatOptions {
    /** Include `forall ...` for quantified variables. Defaults to true. */
    readonly quantify?: boolean;
    /** Sort record labels lexicographically. Defaults to true. */
    readonly sortFields?: boolean;
    /** Append the residual `lacks` constraints. Defaults to false. */
    readonly showLacks?: boolean;
}
export declare function typeToString(value: Type | TypeScheme, options?: FormatOptions): string;
export declare const formatType: typeof typeToString;
export declare const formatScheme: typeof typeToString;
export declare const showScheme: typeof typeToString;
export declare const schemeToString: typeof typeToString;
export declare const formatTypeString: typeof typeToString;
export declare const showType: typeof typeToString;
export declare const prettyType: typeof typeToString;
export declare const typeText: typeof typeToString;
export declare const pretty: typeof typeToString;
export declare const format: typeof typeToString;
export declare const show: typeof typeToString;
