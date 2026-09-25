/**
 * PackBits 风格的 RLE 压缩，用于压缩历史差量负载，降低内存占用。
 * 支持按多字节单元（如 RGBA 像素 = 4 字节）编码，纯色像素流可高效压缩。
 * 编码 token（长度以单元计）：
 *   0..127   -> 后跟 token+1 个字面量单元
 *   128..255 -> 后跟 1 个单元，重复 (token-126) 次（2..129）
 */

function unitEquals(src: Uint8Array | Uint8ClampedArray, a: number, b: number, unit: number): boolean {
  for (let k = 0; k < unit; k++) {
    if (src[a + k] !== src[b + k]) return false;
  }
  return true;
}

export function rleEncode(src: Uint8Array | Uint8ClampedArray, unitSize = 1): Uint8Array {
  if (src.length % unitSize !== 0) {
    throw new RangeError(`length ${src.length} not aligned to unit size ${unitSize}`);
  }
  const out: number[] = [];
  const n = src.length / unitSize; // 单元数
  let i = 0;
  while (i < n) {
    // 探测重复游程（按单元比较）
    let run = 1;
    while (i + run < n && run < 129 && unitEquals(src, i * unitSize, (i + run) * unitSize, unitSize)) {
      run++;
    }
    if (run >= 3) {
      out.push(126 + run);
      for (let k = 0; k < unitSize; k++) out.push(src[i * unitSize + k]);
      i += run;
      continue;
    }
    // 字面量游程：直到遇到 >=3 的重复或达到 128 上限
    const litStart = i;
    let litLen = run;
    i += run;
    while (i < n && litLen < 128) {
      let r = 1;
      while (i + r < n && r < 129 && unitEquals(src, i * unitSize, (i + r) * unitSize, unitSize)) {
        r++;
      }
      if (r >= 3) break;
      litLen += r;
      i += r;
    }
    out.push(litLen - 1);
    for (let k = litStart * unitSize; k < (litStart + litLen) * unitSize; k++) out.push(src[k]);
  }
  return Uint8Array.from(out);
}

export function rleDecode(packed: Uint8Array, unitSize = 1, expectedLength?: number): Uint8ClampedArray {
  const out: number[] = [];
  let i = 0;
  while (i < packed.length) {
    const token = packed[i++];
    if (token <= 127) {
      const len = token + 1;
      for (let k = 0; k < len * unitSize; k++) out.push(packed[i++]);
    } else {
      const len = token - 126;
      const unit: number[] = [];
      for (let k = 0; k < unitSize; k++) unit.push(packed[i++]);
      for (let r = 0; r < len; r++) for (let k = 0; k < unitSize; k++) out.push(unit[k]);
    }
  }
  if (expectedLength !== undefined && out.length !== expectedLength) {
    throw new Error(`RLE decode length mismatch: got ${out.length}, want ${expectedLength}`);
  }
  return Uint8ClampedArray.from(out);
}
