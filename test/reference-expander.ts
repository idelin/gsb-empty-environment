// 参考展开器：不使用压缩器（src/lz77.ts）的任何匹配逻辑，
// 对 token 序列做最朴素的逐字节展开，用于交叉验证。
import type { Token } from '../src/lz77.ts';

export function expandTokens(tokens: Token[]): Uint8Array {
  const out: number[] = [];
  for (const t of tokens) {
    if (t.type === 'lit') {
      out.push(t.value);
    } else {
      if (!Number.isInteger(t.distance) || t.distance < 1 || t.distance > out.length) {
        throw new Error(`参考展开器: 非法距离 ${t.distance}（历史 ${out.length}）`);
      }
      // 重叠复制语义：逐字节向前取，length 可以大于 distance。
      for (let k = 0; k < t.length; k++) {
        out.push(out[out.length - t.distance]);
      }
    }
  }
  return Uint8Array.from(out);
}
