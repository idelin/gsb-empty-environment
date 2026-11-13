export type Amount = bigint | number | string;

export interface Position {
  blockNumber: number;
  txIndex: number;
}

export type PositionInput =
  | Position
  | { block: number; txIndex: number }
  | readonly [blockNumber: number, txIndex: number];

export interface BlockInterval {
  /**
   * Inclusive lower bound.
   */
  startBlock: number;
  /**
   * Exclusive upper bound.
   */
  endBlock: number;
}

export function toBigInt(value: Amount, label = "amount"): bigint {
  if (typeof value === "bigint") {
    if (value < 0n) {
      throw new RangeError(`${label} cannot be negative`);
    }
    return value;
  }

  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new RangeError(`${label} must be a non-negative safe integer`);
    }
    return BigInt(value);
  }

  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!/^[0-9]+$/.test(trimmed)) {
      throw new RangeError(`${label} must be a non-negative integer string`);
    }
    return BigInt(trimmed);
  }

  throw new TypeError(`${label} must be bigint, number, or decimal string`);
}

export function fromBigInt(value: bigint): bigint {
  return value;
}

export function normalizePosition(input: PositionInput): Position {
  let blockNumber: unknown;
  let txIndex: unknown;

  if (Array.isArray(input)) {
    const tuple = input as readonly [number, number];
    blockNumber = tuple[0];
    txIndex = tuple[1];
  } else if (input !== null && typeof input === "object") {
    const positioned = input as {
      blockNumber?: unknown;
      block?: unknown;
      txIndex?: unknown;
    };
    blockNumber =
      "blockNumber" in positioned ? positioned.blockNumber : positioned.block;
    txIndex = positioned.txIndex;
  }

  const normalizedBlock = normalizeBlockNumber(blockNumber, "blockNumber");
  const normalizedTx = normalizeBlockNumber(txIndex, "txIndex");

  return { blockNumber: normalizedBlock, txIndex: normalizedTx };
}

export function normalizeBlockNumber(value: unknown, label = "blockNumber"): number {
  if (typeof value === "string" && !/^[0-9]+$/.test(value.trim())) {
    throw new RangeError(`${label} must be a non-negative safe integer`);
  }

  const numeric =
    typeof value === "bigint"
      ? Number(value)
      : typeof value === "string"
        ? Number(value)
        : value;

  if (
    typeof numeric !== "number" ||
    !Number.isSafeInteger(numeric) ||
    numeric < 0
  ) {
    throw new RangeError(`${label} must be a non-negative safe integer`);
  }

  return numeric;
}

export function comparePositions(left: Position, right: Position): -1 | 0 | 1 {
  if (left.blockNumber < right.blockNumber) return -1;
  if (left.blockNumber > right.blockNumber) return 1;
  if (left.txIndex < right.txIndex) return -1;
  if (left.txIndex > right.txIndex) return 1;
  return 0;
}

export function positionsEqual(left: Position, right: Position): boolean {
  return comparePositions(left, right) === 0;
}

export function positionKey(position: Position): string {
  return `${position.blockNumber}:${position.txIndex}`;
}

export function clonePosition(position: Position): Position {
  return { blockNumber: position.blockNumber, txIndex: position.txIndex };
}

export function blockInterval(startBlock: number, endBlock: number): BlockInterval {
  const start = normalizeBlockNumber(startBlock, "startBlock");
  const end = normalizeBlockNumber(endBlock, "endBlock");
  if (end < start) {
    throw new RangeError("block interval endBlock cannot precede startBlock");
  }
  return { startBlock: start, endBlock: end };
}

/**
 * Block intervals are half-open: [startBlock, endBlock).
 */
export function isBlockInInterval(
  blockNumber: number,
  interval: BlockInterval
): boolean {
  const block = normalizeBlockNumber(blockNumber, "blockNumber");
  return interval.startBlock <= block && block < interval.endBlock;
}

export function assertBlockInInterval(
  blockNumber: number,
  interval: BlockInterval,
  label = "block"
): void {
  if (!isBlockInInterval(blockNumber, interval)) {
    throw new RangeError(
      `${label} ${blockNumber} is outside half-open interval ` +
        `[${interval.startBlock}, ${interval.endBlock})`
    );
  }
}
