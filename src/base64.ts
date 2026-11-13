/**
 * 同构 Base64 解码：浏览器使用 `atob`，Node 使用 Buffer（若存在），
 * 否则回退到内置实现。输入为 MPD `cenc:pssh` 常见的标准 Base64。
 */

const globalAtob: ((input: string) => string) | undefined =
  globalThis.atob?.bind(globalThis);

const globalBuffer = (globalThis as { Buffer?: { from: (input: string, enc: string) => Uint8Array } }).Buffer;

export function base64ToBytes(input: string): Uint8Array {
  const clean = input.replace(/\s+/g, '');
  if (globalBuffer) {
    return globalBuffer.from(clean, 'base64');
  }
  if (globalAtob) {
    const binary = globalAtob(clean);
    const out = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
    return out;
  }
  return decodeFallback(clean);
}

function decodeFallback(input: string): Uint8Array {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  const lookup = new Int8Array(256).fill(-1);
  for (let i = 0; i < alphabet.length; i++) lookup[alphabet.charCodeAt(i)] = i;

  const bytes: number[] = [];
  let buffer = 0;
  let bits = 0;
  for (const char of input) {
    if (char === '=') break;
    const value = lookup[char.charCodeAt(0)];
    if (value < 0) continue;
    buffer = (buffer << 6) | value;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((buffer >> bits) & 0xff);
    }
  }
  return Uint8Array.from(bytes);
}
