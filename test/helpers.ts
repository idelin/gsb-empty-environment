export interface P256KeyPair {
  publicKeySpki: Uint8Array;
  privateKey: CryptoKey;
}

export async function generateP256KeyPair(): Promise<P256KeyPair> {
  const keyPair = await globalThis.crypto.subtle.generateKey(
    { name: "ECDSA", namedCurve: "P-256" },
    false,
    ["sign", "verify"],
  );
  const publicKeySpki = new Uint8Array(
    await globalThis.crypto.subtle.exportKey("spki", keyPair.publicKey),
  );
  return { publicKeySpki, privateKey: keyPair.privateKey };
}

export async function signP256(
  privateKey: CryptoKey,
  data: Uint8Array,
): Promise<Uint8Array> {
  const signature = await globalThis.crypto.subtle.sign(
    { name: "ECDSA", hash: "SHA-256" },
    privateKey,
    data,
  );
  return new Uint8Array(signature);
}

export function bytes(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

export function clone(value: Uint8Array): Uint8Array {
  return new Uint8Array(value);
}

export function flipLastByte(value: Uint8Array): Uint8Array {
  const copy = clone(value);
  copy[copy.length - 1] = (copy[copy.length - 1] ^ 0xff) as number;
  return copy;
}

export function hex(value: Uint8Array): string {
  return Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join("");
}
