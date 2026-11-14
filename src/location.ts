export interface SourceLocation {
  offset: number;
  line: number;
  column: number;
}

export function buildLineStarts(source: string): number[] {
  const starts = [0];
  for (let index = 0; index < source.length; index += 1) {
    if (source.charCodeAt(index) === 10) {
      starts.push(index + 1);
    }
  }
  return starts;
}

export function locationAt(
  lineStarts: readonly number[],
  offset: number,
): SourceLocation {
  const safeOffset = Math.max(0, Math.min(offset, Number.MAX_SAFE_INTEGER));
  let low = 0;
  let high = lineStarts.length - 1;

  while (low <= high) {
    const middle = (low + high) >> 1;
    if (lineStarts[middle] <= safeOffset) {
      low = middle + 1;
    } else {
      high = middle - 1;
    }
  }

  const lineIndex = Math.max(0, high);
  return {
    offset: safeOffset,
    line: lineIndex + 1,
    column: safeOffset - lineStarts[lineIndex] + 1,
  };
}
