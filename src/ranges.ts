/**
 * Compress a set of indexes into the canonical Kubernetes
 * `status.completedIndexes` range string.
 *
 * Rules:
 * - indexes are deduplicated and sorted before compression;
 * - a run of one index renders as that index (`"5"`);
 * - a run of two or more consecutive indexes renders as `"start-end"`;
 * - runs are joined with commas;
 * - an empty set renders as the empty string.
 *
 * Examples:
 *   `[0,1,2,3,5,8,9,10] -> "0-3,5,8-10"`
 *   `[3,1,2]            -> "1-3"`
 *   `[]                 -> ""`
 */
export function compressIndexRanges(indexes: Iterable<number>): string {
  const sorted = [...new Set(indexes)].sort((a, b) => a - b);
  if (sorted.length === 0) {
    return '';
  }

  const parts: string[] = [];
  let runStart = sorted[0];
  let runEnd = sorted[0];

  const flush = (): void => {
    parts.push(runStart === runEnd ? `${runStart}` : `${runStart}-${runEnd}`);
  };

  for (let position = 1; position < sorted.length; position += 1) {
    const current = sorted[position];
    if (current === runEnd + 1) {
      runEnd = current;
    } else {
      flush();
      runStart = current;
      runEnd = current;
    }
  }
  flush();

  return parts.join(',');
}

/** Parse a canonical range string back into the sorted list of indexes. */
export function expandIndexRanges(ranges: string): number[] {
  if (ranges.trim() === '') {
    return [];
  }
  const result: number[] = [];
  for (const part of ranges.split(',')) {
    const token = part.trim();
    if (token.includes('-')) {
      const [startText, endText] = token.split('-');
      const start = Number(startText);
      const end = Number(endText);
      for (let index = start; index <= end; index += 1) {
        result.push(index);
      }
    } else {
      result.push(Number(token));
    }
  }
  return result;
}
