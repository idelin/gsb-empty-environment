/** 两个符号序列的最长公共后缀长度，用于衡量解码器在损坏点之后“重新对齐”的能力。 */
export function longestCommonSuffixLength(a: ArrayLike<number>, b: ArrayLike<number>): number {
  let n = 0;
  while (n < a.length && n < b.length && a[a.length - 1 - n] === b[b.length - 1 - n]) {
    n += 1;
  }
  return n;
}
