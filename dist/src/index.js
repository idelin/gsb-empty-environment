export { CARDINALITY_VIOLATION, CardinalityViolationError, INVALID_MERGE_STATEMENT, MergeError, UNIQUE_VIOLATION, UniqueViolationError, } from "./errors.js";
export { checkUniqueConstraints, findUniqueViolation, normalizeUniqueIndexes, validateNormalizedUniqueConstraints, validateUniqueConstraints, } from "./constraints.js";
export { MergeTable, applyMerge, deleteRow, doNothing, executeMerge, executeSqlMerge, insert, merge, mergeInto, mergeRows, noop, runMerge, update, } from "./merge.js";
import { executeMerge } from "./merge.js";
export default executeMerge;
//# sourceMappingURL=index.js.map