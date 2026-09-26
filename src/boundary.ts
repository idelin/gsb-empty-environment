import type { BoundaryPolicy } from './types.ts';

// 越界坐标 -> 图像内坐标; zero 策略返回 null (贡献 0)
export function mapIndex(i: number, n: number, policy: BoundaryPolicy): number | null {
  if (i >= 0 && i < n) return i;
  switch (policy) {
    case 'zero':
      return null;
    case 'clamp':
      return i < 0 ? 0 : n - 1;
    case 'wrap':
      return ((i % n) + n) % n;
    case 'mirror': {
      if (n <= 1) return 0; // 宽/高为 1 时恒定 0, 不会死循环
      const period = 2 * (n - 1);
      const m = ((i % period) + period) % period;
      return m >= n ? period - m : m;
    }
  }
}

// [a,b) 为采样坐标区间(可能越界), 返回实际读取到的图像坐标包围盒 [lo, hi)。
// 越界部分长度受 halo 限制, 只枚举越界段, 不会退化成 O(n) 或死循环。
export function mappedInterval(a: number, b: number, n: number, policy: BoundaryPolicy): [number, number] {
  let lo = Number.POSITIVE_INFINITY;
  let hi = Number.NEGATIVE_INFINITY;
  const inLo = Math.max(a, 0);
  const inHi = Math.min(b, n);
  if (inLo < inHi) {
    lo = inLo;
    hi = inHi;
  }
  const leftEnd = Math.min(b, 0);
  for (let i = a; i < leftEnd; i++) {
    const m = mapIndex(i, n, policy);
    if (m !== null) {
      if (m < lo) lo = m;
      if (m + 1 > hi) hi = m + 1;
    }
  }
  const rightStart = Math.max(a, n);
  for (let i = rightStart; i < b; i++) {
    const m = mapIndex(i, n, policy);
    if (m !== null) {
      if (m < lo) lo = m;
      if (m + 1 > hi) hi = m + 1;
    }
  }
  if (lo === Number.POSITIVE_INFINITY) return [0, 0];
  return [lo, hi];
}
