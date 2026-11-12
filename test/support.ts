import {
  createPublicKey,
  generateKeyPairSync,
  sign,
  verify,
  createHash,
  type KeyObject,
} from 'node:crypto';
import { encodeData } from '../src/codec.ts';
import {
  HashScheme,
  SignatureScheme,
  type Message,
  type MessageData,
} from '../src/types.ts';

export interface Ed25519Key {
  publicKey: Uint8Array;
  privateKey: KeyObject;
  sign: (digest: Uint8Array) => Uint8Array;
}

export function generateEd25519Key(): Ed25519Key {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const jwk = publicKey.export({ format: 'jwk' });
  const rawPublicKey = new Uint8Array(Buffer.from(jwk.x as string, 'base64url'));
  return {
    publicKey: rawPublicKey,
    privateKey,
    sign: (digest) =>
      new Uint8Array(sign(null, Buffer.from(digest), privateKey)),
  };
}

export function sha256(bytes: Uint8Array): Uint8Array {
  return new Uint8Array(createHash('sha256').update(Buffer.from(bytes)).digest());
}

export interface BuildOptions {
  dataBytes?: Uint8Array;
  hash?: Uint8Array;
  signerKey?: Ed25519Key;
}

/** 构造一条密码学上自洽的消息，便于在单个用例里定向篡改某一字段。 */
export function buildMessage(
  data: MessageData,
  signingKey: Ed25519Key,
  options: BuildOptions = {},
): Message {
  const dataBytes = options.dataBytes ?? encodeData(data);
  const hash = options.hash ?? sha256(dataBytes);
  const effectiveKey = options.signerKey ?? signingKey;
  const signature = effectiveKey.sign(hash);
  return {
    data,
    dataBytes,
    hash,
    hashScheme: HashScheme.SHA256,
    signature,
    signatureScheme: SignatureScheme.ED25519,
    signer: effectiveKey.publicKey,
  };
}

/** 直接由裸 Ed25519 公钥（32 字节）构造 KeyObject。 */
export function publicKeyFromRaw(rawPublicKey: Uint8Array): KeyObject {
  return createPublicKey({
    key: Buffer.concat([
      Buffer.from('302a300506032b6570032100', 'hex'),
      Buffer.from(rawPublicKey),
    ]),
    format: 'der',
    type: 'spki',
  });
}

/** 不做密钥注册：凡能构造出 Ed25519 公钥的签名者都参与验签。 */
export function verifyAnyEd25519(input: {
  hash: Uint8Array;
  signature: Uint8Array;
  signer: Uint8Array;
}): boolean {
  try {
    return verify(
      null,
      Buffer.from(input.hash),
      publicKeyFromRaw(input.signer),
      Buffer.from(input.signature),
    );
  } catch {
    return false;
  }
}

export function cloneMessage(message: Message): Message {
  return {
    ...message,
    data: structuredClone(message.data),
    dataBytes: new Uint8Array(message.dataBytes),
    hash: new Uint8Array(message.hash),
    signature: new Uint8Array(message.signature),
    signer: new Uint8Array(message.signer),
  };
}
