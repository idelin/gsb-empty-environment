/**
 * 纯 TypeScript 的 secp256k1 最小实现（仿射坐标 + 费马逆元），
 * 支持 RFC6979 确定性签名与公钥恢复，仅用于测试注入。
 * 生产环境请使用 audited 库实现 RecoverAddressFn。
 */
import { createHmac, randomBytes } from "node:crypto";
import { bytesToHex } from "../../src/bytes.js";
import { keccak256 } from "./keccak.js";

const P = 0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffefffffc2fn;
const N = 0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141n;
const A = 0n;
const B = 7n;
const GX = 0x79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798n;
const GY = 0x483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8n;

interface Point {
  x: bigint;
  y: bigint;
}

const INFINITY: unique symbol = Symbol("infinity");
type MaybePoint = Point | typeof INFINITY;

function mod(value: bigint, modulus: bigint = P): bigint {
  const result = value % modulus;
  return result >= 0n ? result : result + modulus;
}

function modInverse(value: bigint, modulus: bigint): bigint {
  let a = mod(value, modulus);
  let b = modulus;
  let x0 = 1n;
  let x1 = 0n;
  while (a !== 0n) {
    const quotient = b / a;
    [a, b] = [b - quotient * a, a];
    [x0, x1] = [x1 - quotient * x0, x0];
  }
  if (b !== 1n) {
    throw new Error("modular inverse does not exist");
  }
  return mod(x1, modulus);
}

function isOnCurve(point: Point): boolean {
  return mod(point.y * point.y - (point.x ** 3n + A * point.x + B)) === 0n;
}

function pointAdd(p: MaybePoint, q: MaybePoint): MaybePoint {
  if (p === INFINITY) return q;
  if (q === INFINITY) return p;
  if (p.x === q.x && mod(p.y + q.y) === 0n) return INFINITY;

  let slope: bigint;
  if (p.x === q.x && p.y === q.y) {
    slope = mod((3n * p.x * p.x) * modInverse(2n * p.y, P));
  } else {
    slope = mod((q.y - p.y) * modInverse(q.x - p.x, P));
  }
  const x = mod(slope * slope - p.x - q.x);
  const y = mod(slope * (p.x - x) - p.y);
  return { x, y };
}

function scalarMultiply(scalar: bigint, point: MaybePoint): MaybePoint {
  let result: MaybePoint = INFINITY;
  let addend: MaybePoint = point;
  let remaining = mod(scalar, N);
  while (remaining > 0n) {
    if (remaining & 1n) {
      result = pointAdd(result, addend);
    }
    addend = pointAdd(addend, addend);
    remaining >>= 1n;
  }
  return result;
}

const GENERATOR: Point = { x: GX, y: GY };

function publicKeyFromPrivateKey(privateKey: bigint): Point {
  const point = scalarMultiply(privateKey, GENERATOR);
  if (point === INFINITY || !isOnCurve(point)) {
    throw new Error("invalid private key");
  }
  return point;
}

function to32(value: bigint): Uint8Array {
  const out = new Uint8Array(32);
  let remaining = value;
  for (let i = 31; i >= 0 && remaining > 0n; i -= 1) {
    out[i] = Number(remaining & 0xffn);
    remaining >>= 8n;
  }
  return out;
}

function bytesToBigInt(bytes: Uint8Array): bigint {
  let value = 0n;
  for (const byte of bytes) {
    value = (value << 8n) | BigInt(byte);
  }
  return value;
}

/** RFC6979 确定性 k（HMAC-SHA256，qlen=256 且与 hlen 相等，无需额外截断）。 */
function deterministicK(privateKey: bigint, digest: Uint8Array): bigint {
  let v = new Uint8Array(32).fill(0x01);
  let k = new Uint8Array(32).fill(0x00);
  const hmac = (key: Uint8Array, ...data: Uint8Array[]): Uint8Array => {
    const h = createHmac("sha256", key);
    for (const chunk of data) h.update(chunk);
    return new Uint8Array(h.digest());
  };
  k = hmac(k, v, new Uint8Array([0x00]), to32(privateKey), digest);
  v = hmac(k, v);
  k = hmac(k, v, new Uint8Array([0x01]), to32(privateKey), digest);
  v = hmac(k, v);
  for (;;) {
    v = hmac(k, v);
    const candidate = bytesToBigInt(v);
    if (candidate >= 1n && candidate < N) {
      return candidate;
    }
    k = hmac(k, v, new Uint8Array([0x00]));
    v = hmac(k, v);
  }
}

export interface Signature {
  r: bigint;
  s: bigint;
  recoveryId: number;
}

/** 对 32 字节摘要做确定性 ECDSA 签名，返回低 s 形式与 recovery id。 */
export function signDigest(digest: Uint8Array, privateKey: bigint): Signature {
  const e = bytesToBigInt(digest) % N;
  for (;;) {
    const k = deterministicK(privateKey, digest);
    const point = scalarMultiply(k, GENERATOR);
    if (point === INFINITY) continue;
    const r = mod(point.x, N);
    if (r === 0n) continue;
    const sRaw = mod(modInverse(k, N) * (e + r * privateKey), N);
    if (sRaw === 0n) continue;

    let s = sRaw;
    // r 取点 x 坐标且始终小于 P，不会出现 x = r + n 的高位情形，
    // 因此 recovery id 只含 y 奇偶位（0/1）。
    let recoveryId = (point.y & 1n) === 0n ? 0 : 1;

    // 强制低 s（EIP-2），翻转时同步翻转 recovery id。
    if (s > N / 2n) {
      s = N - s;
      recoveryId ^= 1;
    }
    return { r, s, recoveryId };
  }
}

/** 签名序列化为 65 字节 r||s||v，v 取 27/28（以太坊惯例）。 */
export function signatureToBytes(signature: Signature): Uint8Array {
  const out = new Uint8Array(65);
  out.set(to32(signature.r), 0);
  out.set(to32(signature.s), 32);
  out[64] = signature.recoveryId + 27;
  return out;
}

/** 把低 s 签名翻转为高 s 形式（仅测试高 s 拒绝路径用），v 同步翻转。 */
export function toHighS(signature: Signature): Signature {
  return {
    r: signature.r,
    s: N - signature.s,
    recoveryId: signature.recoveryId ^ 1,
  };
}

/** 公钥恢复：由摘要、r/s 与 recovery id（0/1）还原 R 点并推导 Q。 */
export function recoverPublicKey(
  digest: Uint8Array,
  r: bigint,
  s: bigint,
  recoveryId: number,
): Point {
  if (recoveryId !== 0 && recoveryId !== 1) {
    throw new Error("only recovery ids 0/1 (no overflow) are supported");
  }
  const x = r + (recoveryId & 2 ? N : 0n);
  if (x >= P) {
    throw new Error("recovered x coordinate out of field range");
  }
  const ySquared = mod(x ** 3n + B);
  let y = modPow(ySquared, (P + 1n) / 4n, P);
  const yParity = (y & 1n) === 1n ? 1 : 0;
  if (yParity !== (recoveryId & 1)) {
    y = P - y;
  }
  const rPoint: Point = { x, y };
  if (!isOnCurve(rPoint)) {
    throw new Error("recovered point is not on the curve");
  }
  const e = bytesToBigInt(digest) % N;
  const rInverse = modInverse(r, N);
  const q = pointAdd(
    scalarMultiply(mod(rInverse * s, N), rPoint),
    scalarMultiply(mod(-rInverse * e, N), GENERATOR),
  );
  if (q === INFINITY) {
    throw new Error("public key recovery produced the point at infinity");
  }
  return q;
}

function modPow(base: bigint, exponent: bigint, modulus: bigint): bigint {
  let result = 1n;
  let b = mod(base, modulus);
  let exp = exponent;
  while (exp > 0n) {
    if (exp & 1n) result = mod(result * b, modulus);
    b = mod(b * b, modulus);
    exp >>= 1n;
  }
  return result;
}
/** 以太坊地址 = 最后 20 字节 keccak256(公钥 x||y)。 */
export function publicKeyToAddress(point: Point): string {
  const pub = concat(to32(point.x), to32(point.y));
  return bytesToHex(keccak256(pub).slice(12));
}

/** 适配 RecoverAddressFn 的注入实现。signature 为 r||s||v(0/1)。 */
export function recoverAddress(
  digest: Uint8Array,
  signature: Uint8Array,
): string {
  if (signature.length !== 65) {
    throw new Error("compact signature must be 65 bytes");
  }
  const r = bytesToBigInt(signature.slice(0, 32));
  const s = bytesToBigInt(signature.slice(32, 64));
  const rawV = signature[64];
  // 同时容忍以太坊惯例 v=27/28 与紧凑形式 v=0/1。
  const recoveryId = rawV === 27 || rawV === 28 ? rawV - 27 : rawV;
  return publicKeyToAddress(recoverPublicKey(digest, r, s, recoveryId));
}

/** 生成随机私钥（测试用）。 */
export function randomPrivateKey(): bigint {
  for (;;) {
    const candidate = bytesToBigInt(new Uint8Array(randomBytes(32)));
    if (candidate >= 1n && candidate < N) return candidate;
  }
}

/** 私钥对应地址。 */
export function addressFromPrivateKey(privateKey: bigint): string {
  return publicKeyToAddress(publicKeyFromPrivateKey(privateKey));
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}
