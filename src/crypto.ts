import crypto from 'node:crypto';

export function randomKey(bytes = 32): Buffer {
  return crypto.randomBytes(bytes);
}

export function hmacSha256(key: Buffer, data: string | Buffer): Buffer {
  return crypto.createHmac('sha256', key).update(data).digest();
}

export function hkdf(master: Buffer, info: string, length = 32): Buffer {
  const derived = crypto.hkdfSync('sha256', master, Buffer.alloc(0), Buffer.from(info, 'utf8'), length);
  return Buffer.from(derived);
}

export function sha256Hex(data: string | Buffer): string {
  return crypto.createHash('sha256').update(data).digest('hex');
}

export interface EciesBox {
  ephemeralPublicKey: string;
  iv: string;
  tag: string;
  ciphertext: string;
}

export function generateEcKeyPair(): { publicKeyPem: string; privateKeyPem: string } {
  const { publicKey, privateKey } = crypto.generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
  return {
    publicKeyPem: publicKey.export({ type: 'spki', format: 'pem' }).toString(),
    privateKeyPem: privateKey.export({ type: 'pkcs8', format: 'pem' }).toString(),
  };
}

function ecdhSharedSecret(privateKeyPem: string, publicKeyPem: string): Buffer {
  const privateKey = crypto.createPrivateKey(privateKeyPem);
  const publicKey = crypto.createPublicKey(publicKeyPem);
  return crypto.diffieHellman({ privateKey, publicKey });
}

export function eciesEncrypt(recipientPublicKeyPem: string, plaintext: Buffer): EciesBox {
  const ephemeral = generateEcKeyPair();
  const shared = ecdhSharedSecret(ephemeral.privateKeyPem, recipientPublicKeyPem);
  const key = hkdf(shared, 'tracevault-ecies');
  const iv = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv('aes-256-gcm', key, iv);
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  return {
    ephemeralPublicKey: ephemeral.publicKeyPem,
    iv: iv.toString('base64'),
    tag: cipher.getAuthTag().toString('base64'),
    ciphertext: ciphertext.toString('base64'),
  };
}

export function eciesDecrypt(recipientPrivateKeyPem: string, box: EciesBox): Buffer {
  const shared = ecdhSharedSecret(recipientPrivateKeyPem, box.ephemeralPublicKey);
  const key = hkdf(shared, 'tracevault-ecies');
  const decipher = crypto.createDecipheriv('aes-256-gcm', key, Buffer.from(box.iv, 'base64'));
  decipher.setAuthTag(Buffer.from(box.tag, 'base64'));
  return Buffer.concat([decipher.update(Buffer.from(box.ciphertext, 'base64')), decipher.final()]);
}

export function signPayload(privateKeyPem: string, payload: string): string {
  return crypto.sign('sha256', Buffer.from(payload, 'utf8'), privateKeyPem).toString('base64');
}

export function verifySignature(publicKeyPem: string, payload: string, signature: string): boolean {
  return crypto.verify('sha256', Buffer.from(payload, 'utf8'), publicKeyPem, Buffer.from(signature, 'base64'));
}
