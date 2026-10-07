export { paginate } from "./paginate.ts";
export { enumeratePartitions, verifyWithEnumeration } from "./enumerate.ts";
export { PaginationOverflowError } from "./errors.ts";
export { row, line, group, table, blockHeight } from "./builders.ts";
export type {
  RowSpec,
  BlockSpec,
  TableSpec,
  PaginateOptions,
  PlacedRow,
  PlacedBlock,
  PageLayout,
  PaginationResult,
} from "./types.ts";
export type { EnumerationReport } from "./enumerate.ts";
