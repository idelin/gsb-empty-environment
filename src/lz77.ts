// LZ77 分词器（仅编码端使用）：贪心哈希链匹配。
// 解码端不依赖本文件的任何逻辑。
export type Token =
  | { type: 'lit'; value: number }
  | { type: 'match'; length: number; distance: number };

export const MIN_MATCH = 3;
export const MAX_MATCH = 258;
export const MAX_WINDOW = 65535;

export function tokenize(
  data: Uint8Array,
  windowLimit: number,
  maxChain: number = 256,
): Token[] {
  const n = data.length;
  const tokens: Token[] = [];
  if (n === 0) return tokens;

  const HASH_BITS = 15;
  const HASH_SIZE = 1 << HASH_BITS;
  const head = new Int32Array(HASH_SIZE).fill(-1);
  const prev = new Int32Array(n).fill(-1);

  const hashAt = (i: number): number =>
    ((((data[i] << 16) ^ (data[i + 1] << 8) ^ data[i + 2]) * 2654435761) >>> 0) &
    (HASH_SIZE - 1);

  const insert = (i: number): void => {
    if (i + MIN_MATCH > n) return;
    const h = hashAt(i);
    prev[i] = head[h];
    head[h] = i;
  };

  let i = 0;
  while (i < n) {
    let bestLen = 0;
    let bestDist = 0;
    if (i + MIN_MATCH <= n) {
      const maxLen = Math.min(MAX_MATCH, n - i);
      let j = head[hashAt(i)];
      let tries = maxChain;
      while (j >= 0 && tries-- > 0) {
        const dist = i - j;
        if (dist > windowLimit) break;
        // 快速过滤：当前最佳长度的下一个字节相同才值得展开比较。
        if (bestLen === 0 || bestLen >= maxLen || data[j + bestLen] === data[i + bestLen]) {
          let l = 0;
          // 与源缓冲区直接比较，天然支持 length > distance 的重叠匹配。
          while (l < maxLen && data[j + l] === data[i + l]) l++;
          if (l > bestLen) {
            bestLen = l;
            bestDist = dist;
            if (l >= maxLen) break;
          }
        }
        j = prev[j];
      }
    }
    if (bestLen >= MIN_MATCH) {
      tokens.push({ type: 'match', length: bestLen, distance: bestDist });
      const end = i + bestLen;
      while (i < end) {
        insert(i);
        i++;
      }
    } else {
      tokens.push({ type: 'lit', value: data[i] });
      insert(i);
      i++;
    }
  }
  return tokens;
}
