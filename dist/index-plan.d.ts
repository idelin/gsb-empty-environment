import type { ContainmentQuery, IndexExtractionOptions, IndexPlan, IndexTerm, JsonValue, PathInput } from "./types.js";
export declare function extractIndexPlan(input: PathInput, options?: IndexExtractionOptions): IndexPlan;
export declare const extractJsonPathIndexPlan: typeof extractIndexPlan;
export declare const indexPlan: typeof extractIndexPlan;
export declare function extractIndexTerms(input: PathInput): IndexTerm[];
export declare function matchesIndexPlan(document: JsonValue, plan: IndexPlan): boolean;
export declare const indexPlanMatchesDocument: typeof matchesIndexPlan;
export declare const matchesPlan: (plan: IndexPlan, document: JsonValue) => boolean;
export declare function toJsonbContainment(plan: IndexPlan): ContainmentQuery;
export declare const toContainmentQuery: typeof toJsonbContainment;
//# sourceMappingURL=index-plan.d.ts.map