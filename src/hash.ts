export type DigestFn = (data: Uint8Array) => string;

export function fnv1aDigest(data: Uint8Array): string {
  let h1 = 0x811c9dc5 >>> 0;
  let h2 = 0x01000193 >>> 0;
  for (let i = 0; i < data.length; i++) {
    h1 ^= data[i];
    h1 = Math.imul(h1, 0x01000193) >>> 0;
    h2 ^= (data[i] + i) & 0xff;
    h2 = Math.imul(h2, 0x85ebca6b) >>> 0;
  }
  const a = h1.toString(16).padStart(8, "0");
  const b = h2.toString(16).padStart(8, "0");
  return a + b;
}

export function constantDigest(_data: Uint8Array): string {
  return "0000000000000000";
}
