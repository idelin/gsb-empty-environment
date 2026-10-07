/**
 * Thrown when a single unbreakable block (merged-cell row group) plus the
 * repeated header can never fit on one page. Without this guard a paginator
 * would keep emitting empty pages forever.
 */
export class PaginationOverflowError extends Error {
  readonly blockId: string;
  readonly blockIndex: number;
  readonly rowIds: string[];
  readonly required: number;
  readonly available: number;

  constructor(args: {
    blockId: string;
    blockIndex: number;
    rowIds: string[];
    required: number;
    available: number;
  }) {
    super(
      `Block "${args.blockId}" (index ${args.blockIndex}, rows [${args.rowIds.join(", ")}]) ` +
        `requires ${args.required} but only ${args.available} is available per page ` +
        `(pageHeight - headerHeight). The group is unbreakable; enlarge the page, ` +
        `shrink the header, or split the merged-cell group.`,
    );
    this.name = "PaginationOverflowError";
    this.blockId = args.blockId;
    this.blockIndex = args.blockIndex;
    this.rowIds = args.rowIds;
    this.required = args.required;
    this.available = args.available;
  }
}
