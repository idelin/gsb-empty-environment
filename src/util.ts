/** 确定性伪随机数据生成器（splitmix32），用于可复现的测试与样例。 */
export function pseudoRandomBytes(length: number, seed: number): Buffer {
  const out = Buffer.allocUnsafe(length);
  let state = seed >>> 0;
  let word = 0;
  for (let i = 0; i < length; i++) {
    if (i % 4 === 0) {
      state = (state + 0x9e3779b9) >>> 0;
      let z = state;
      z = Math.imul(z ^ (z >>> 16), 0x21f0aaad);
      z = Math.imul(z ^ (z >>> 15), 0x735a2d97);
      word = (z ^ (z >>> 15)) >>> 0;
    }
    out[i] = word & 0xff;
    word >>>= 8;
  }
  return out;
}
