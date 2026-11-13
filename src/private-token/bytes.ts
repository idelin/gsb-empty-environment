export function concatBytes(chunks: Uint8Array[]): Uint8Array {
  const totalLength = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const output = new Uint8Array(totalLength);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.length;
  }
  return output;
}

export function bytesEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) {
    return false;
  }
  let diff = 0;
  for (let index = 0; index < left.length; index += 1) {
    diff |= (left[index] ?? 0) ^ (right[index] ?? 0);
  }
  return diff === 0;
}

export function copyBytes(input: Uint8Array): Uint8Array {
  const output = new Uint8Array(input.length);
  output.set(input);
  return output;
}

export function toHex(input: Uint8Array): string {
  let output = "";
  for (const byte of input) {
    output += byte.toString(16).padStart(2, "0");
  }
  return output;
}

export function fromHex(input: string): Uint8Array {
  if (input.length % 2 !== 0) {
    throw new Error("Hex input must contain an even number of characters");
  }
  const output = new Uint8Array(input.length / 2);
  for (let index = 0; index < output.length; index += 1) {
    const high = parseHexDigit(input.charCodeAt(index * 2));
    const low = parseHexDigit(input.charCodeAt(index * 2 + 1));
    output[index] = (high << 4) | low;
  }
  return output;
}

function parseHexDigit(code: number): number {
  if (code >= 48 && code <= 57) {
    return code - 48;
  }
  if (code >= 65 && code <= 70) {
    return code - 65 + 10;
  }
  if (code >= 97 && code <= 102) {
    return code - 97 + 10;
  }
  throw new Error("Invalid hex character");
}
