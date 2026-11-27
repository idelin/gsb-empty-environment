import assert from "node:assert/strict";
import {
  createCipheriv,
  createHmac,
  diffieHellman,
  generateKeyPairSync,
  pbkdf2Sync,
  publicEncrypt,
  randomBytes,
  constants as cryptoConstants,
} from "node:crypto";
import test from "node:test";
import { deflateRawSync, deflateSync } from "node:zlib";
import {
  boundedInflate,
  decryptJwe,
  deriveConcatKdf,
  JweSecurityError,
  parseCompactJwe,
  validateEcdhEpk,
  validateJwe,
  type JwePolicy,
} from "../src/index.ts";

const AES_KW_IV = Buffer.from("a6a6a6a6a6a6a6a6", "hex");

function b64(value: Buffer | Uint8Array | string): string {
  return Buffer.from(value).toString("base64url");
}

function compact(
  headerText: string,
  encryptedKey: Buffer = Buffer.alloc(0),
  iv: Buffer = Buffer.alloc(12, 1),
  ciphertext: Buffer = Buffer.from("x"),
  tag: Buffer = Buffer.alloc(16, 2),
): string {
  return [
    b64(headerText),
    b64(encryptedKey),
    b64(iv),
    b64(ciphertext),
    b64(tag),
  ].join(".");
}

function expectCode(
  operation: () => unknown,
  code: string,
): JweSecurityError {
  try {
    operation();
  } catch (error) {
    assert.ok(error instanceof JweSecurityError);
    assert.equal(error.code, code);
    return error;
  }
  assert.fail(`expected ${code}`);
}

async function expectRejectCode(
  operation: () => Promise<unknown>,
  code: string,
): Promise<JweSecurityError> {
  try {
    await operation();
  } catch (error) {
    assert.ok(error instanceof JweSecurityError);
    assert.equal(error.code, code);
    return error;
  }
  assert.fail(`expected ${code}`);
}

function encryptGcm(options: {
  readonly headerText: string;
  readonly cek: Buffer;
  readonly plaintext: Buffer;
  readonly encryptedKey?: Buffer;
  readonly enc?: string;
}): string {
  const headerSegment = b64(options.headerText);
  const iv = randomBytes(12);
  const cipher = createCipheriv(
    options.enc === "A128GCM" ? "aes-128-gcm" : "aes-256-gcm",
    options.cek,
    iv,
    { authTagLength: 16 },
  );
  cipher.setAAD(Buffer.from(headerSegment, "ascii"));
  const ciphertext = Buffer.concat([
    cipher.update(options.plaintext),
    cipher.final(),
  ]);
  const tag = cipher.getAuthTag();
  return [
    headerSegment,
    b64(options.encryptedKey ?? Buffer.alloc(0)),
    b64(iv),
    b64(ciphertext),
    b64(tag),
  ].join(".");
}

function aesKeyWrap(kek: Buffer, cek: Buffer): Buffer {
  const cipher = createCipheriv(
    `aes${kek.length * 8}-wrap`,
    kek,
    AES_KW_IV,
  );
  return Buffer.concat([cipher.update(cek), cipher.final()]);
}

function encryptCbcHs(options: {
  readonly headerText: string;
  readonly cek: Buffer;
  readonly plaintext: Buffer;
}): string {
  const headerSegment = b64(options.headerText);
  const iv = randomBytes(16);
  const macKey = options.cek.subarray(0, 16);
  const contentKey = options.cek.subarray(16);
  const cipher = createCipheriv("aes-128-cbc", contentKey, iv);
  const ciphertext = Buffer.concat([
    cipher.update(options.plaintext),
    cipher.final(),
  ]);
  const tag = createHmac("sha256", macKey)
    .update(Buffer.from(headerSegment, "ascii"))
    .update(iv)
    .update(ciphertext)
    .digest()
    .subarray(0, 16);

  return [
    headerSegment,
    "",
    b64(iv),
    b64(ciphertext),
    b64(tag),
  ].join(".");
}

test("strict compact parsing rejects count, padding, whitespace, and duplicate JSON keys", () => {
  const valid = compact('{"alg":"dir","enc":"A256GCM"}');
  assert.equal(validateJwe(valid).algorithm, "dir");

  expectCode(() => validateJwe("a.b.c.d"), "JWE_SEGMENT_COUNT");
  expectCode(() => validateJwe("a.b.c.d.e.f"), "JWE_SEGMENT_COUNT");
  expectCode(() => validateJwe(`${valid}=`), "JWE_TAG_INVALID");
  expectCode(() => validateJwe(` ${valid}`), "JWE_BASE64URL_NON_CANONICAL");
  expectCode(() => validateJwe(`${valid}\n`), "JWE_TAG_INVALID");

  const headerPadding = [
    `${b64('{"alg":"dir","enc":"A256GCM"}')}=`,
    "",
    b64(Buffer.alloc(12)),
    b64(Buffer.from("x")),
    b64(Buffer.alloc(16)),
  ].join(".");
  expectCode(
    () => validateJwe(headerPadding),
    "JWE_BASE64URL_NON_CANONICAL",
  );

  const nonCanonicalIv = [
    b64('{"alg":"dir","enc":"A256GCM"}'),
    "",
    "AB",
    b64(Buffer.from("x")),
    b64(Buffer.alloc(16)),
  ].join(".");
  expectCode(() => validateJwe(nonCanonicalIv), "JWE_IV_INVALID");

  expectCode(
    () =>
      validateJwe(
        compact('{"alg":"dir","enc":"A256GCM","alg":"dir"}'),
      ),
    "JWE_HEADER_DUPLICATE_KEY",
  );
  expectCode(
    () =>
      validateJwe(
        compact(
          '{"alg":"dir","enc":"A256GCM","ext":{"nested":1,"nested":2}}',
        ),
      ),
    "JWE_HEADER_DUPLICATE_KEY",
  );
  expectCode(
    () => validateJwe(compact('{"alg":"dir","enc":"A256GCM",}')),
    "JWE_HEADER_JSON_INVALID",
  );
  expectCode(
    () => validateJwe(compact('["dir","A256GCM"]')),
    "JWE_HEADER_NOT_OBJECT",
  );
  const deeplyNested = `{"alg":"dir","enc":"A256GCM","x":${"[".repeat(100)}${"]".repeat(100)}}`;
  expectCode(
    () => validateJwe(compact(deeplyNested)),
    "JWE_HEADER_JSON_INVALID",
  );
  expectCode(
    () =>
      validateJwe(
        [
          b64(Buffer.from([0xff, 0xfe])),
          "",
          b64(Buffer.alloc(12)),
          b64(Buffer.from("x")),
          b64(Buffer.alloc(16)),
        ].join("."),
      ),
    "JWE_HEADER_UTF8_INVALID",
  );
});

test("algorithm and encryption allowlists deny everything outside policy", () => {
  const token = compact('{"alg":"dir","enc":"A256GCM"}');

  assert.equal(
    validateJwe(token, {
      allowedAlg: ["dir"],
      allowedEnc: ["A256GCM"],
    }).encryption,
    "A256GCM",
  );
  expectCode(
    () =>
      validateJwe(token, {
        allowedAlgorithms: ["PBES2-HS256+A128KW"],
      }),
    "JWE_ALGORITHM_NOT_ALLOWED",
  );
  expectCode(
    () =>
      validateJwe(token, {
        allowedEncryptions: ["A128GCM"],
      }),
    "JWE_ENCRYPTION_NOT_ALLOWED",
  );
  expectCode(
    () => validateJwe(token, { allowedAlgorithms: [] }),
    "JWE_ALGORITHM_NOT_ALLOWED",
  );
});

test("PBES2 p2c and p2s limits stop CPU and salt abuse before derivation", () => {
  const p2s = Buffer.alloc(12, 7);
  const hugeIterations = compact(
    JSON.stringify({
      alg: "PBES2-HS256+A128KW",
      enc: "A256GCM",
      p2s: b64(p2s),
      p2c: 1_000_000_000,
    }),
  );

  const start = performance.now();
  const error = expectCode(
    () => validateJwe(hugeIterations),
    "JWE_PBES2_P2C_INVALID",
  );
  const elapsed = performance.now() - start;
  assert.equal(error.code, "JWE_PBES2_P2C_INVALID");
  assert.ok(elapsed < 250, `p2c rejection took ${elapsed}ms`);

  const lowIterations = compact(
    JSON.stringify({
      alg: "PBES2-HS256+A128KW",
      enc: "A256GCM",
      p2s: b64(p2s),
      p2c: 9_999,
    }),
  );
  expectCode(
    () => validateJwe(lowIterations),
    "JWE_PBES2_P2C_INVALID",
  );

  const floatIterations = compact(
    JSON.stringify({
      alg: "PBES2-HS256+A128KW",
      enc: "A256GCM",
      p2s: b64(p2s),
      p2c: 10_000.5,
    }),
  );
  expectCode(
    () => validateJwe(floatIterations),
    "JWE_PBES2_P2C_INVALID",
  );

  const exponentIterations = compact(
    `{"alg":"PBES2-HS256+A128KW","enc":"A256GCM","p2s":${JSON.stringify(
      b64(p2s),
    )},"p2c":1e9}`,
  );
  expectCode(
    () => validateJwe(exponentIterations),
    "JWE_PBES2_P2C_INVALID",
  );

  const aliasRangeToken = compact(
    JSON.stringify({
      alg: "PBES2-HS256+A128KW",
      enc: "A256GCM",
      p2s: b64(p2s),
      p2c: 10_000,
    }),
  );
  expectCode(
    () => validateJwe(aliasRangeToken, { p2cRange: [20_000, 50_000] }),
    "JWE_PBES2_P2C_INVALID",
  );
  expectCode(
    () => validateJwe(aliasRangeToken, { minIter: 20_000 }),
    "JWE_PBES2_P2C_INVALID",
  );

  const shortSalt = compact(
    JSON.stringify({
      alg: "PBES2-HS256+A128KW",
      enc: "A256GCM",
      p2s: b64(Buffer.alloc(4)),
      p2c: 10_000,
    }),
  );
  expectCode(
    () => validateJwe(shortSalt),
    "JWE_PBES2_SALT_TOO_SHORT",
  );

  const paddedSalt = compact(
    JSON.stringify({
      alg: "PBES2-HS256+A128KW",
      enc: "A256GCM",
      p2s: `${b64(p2s)}=`,
      p2c: 10_000,
    }),
  );
  expectCode(
    () => validateJwe(paddedSalt),
    "JWE_PBES2_P2S_INVALID",
  );
});

test("critical extensions are non-empty, standard-header-free, and registered", () => {
  expectCode(
    () =>
      validateJwe(
        compact('{"alg":"dir","enc":"A256GCM","crit":[]}'),
      ),
    "JWE_CRIT_INVALID",
  );
  expectCode(
    () =>
      validateJwe(
        compact('{"alg":"dir","enc":"A256GCM","crit":["alg"]}'),
      ),
    "JWE_CRIT_STANDARD_HEADER",
  );
  expectCode(
    () =>
      validateJwe(
        compact(
          '{"alg":"dir","enc":"A256GCM","crit":["acme"],"acme":true}',
        ),
      ),
    "JWE_CRIT_UNREGISTERED",
  );
  expectCode(
    () =>
      validateJwe(
        compact(
          '{"alg":"dir","enc":"A256GCM","crit":["acme","acme"],"acme":true}',
        ),
        { registeredCriticalExtensions: ["acme"] },
      ),
    "JWE_CRIT_DUPLICATE",
  );
  expectCode(
    () =>
      validateJwe(
        compact(
          '{"alg":"dir","enc":"A256GCM","crit":["acme"]}',
        ),
        { registeredCriticalExtensions: ["acme"] },
      ),
    "JWE_CRIT_EXTENSION_MISSING",
  );
  expectCode(
    () =>
      validateJwe(
        compact('{"alg":"dir","enc":"A256GCM","crit":"acme"}'),
      ),
    "JWE_CRIT_INVALID",
  );

  const accepted = compact(
    '{"alg":"dir","enc":"A256GCM","crit":["acme"],"acme":{"nonce":"123"}}',
  );
  assert.deepEqual(
    validateJwe(accepted, {
      registeredCriticalExtensions: ["acme"],
    }).protectedHeader.acme,
    Object.assign(Object.create(null), { nonce: "123" }),
  );
});

test("ECDH epk is checked in the P-256 field and against the curve equation", () => {
  const { publicKey } = generateKeyPairSync("ec", {
    namedCurve: "prime256v1",
  });
  const validJwk = publicKey.export({ format: "jwk" });

  assert.equal(
    validateEcdhEpk(validJwk).xBytes.length,
    32,
  );

  const validToken = compact(
    JSON.stringify({
      alg: "ECDH-ES",
      enc: "A256GCM",
      epk: validJwk,
    }),
  );
  assert.equal(validateJwe(validToken).algorithm, "ECDH-ES");

  const offCurve = {
    kty: "EC",
    crv: "P-256",
    x: b64(Buffer.concat([Buffer.alloc(31), Buffer.from([1])])),
    y: b64(Buffer.concat([Buffer.alloc(31), Buffer.from([1])])),
  };
  const forged = compact(
    JSON.stringify({
      alg: "ECDH-ES",
      enc: "A256GCM",
      epk: offCurve,
    }),
  );
  expectCode(
    () => validateJwe(forged),
    "JWE_ECDH_EPK_CURVE",
  );

  const infinity = {
    ...offCurve,
    x: b64(Buffer.alloc(32)),
    y: b64(Buffer.alloc(32)),
  };
  expectCode(
    () =>
      validateJwe(
        compact(
          JSON.stringify({
            alg: "ECDH-ES",
            enc: "A256GCM",
            epk: infinity,
          }),
        ),
      ),
    "JWE_ECDH_EPK_INFINITY",
  );

  const outOfRange = {
    ...offCurve,
    x: b64(Buffer.alloc(32, 0xff)),
    y: b64(Buffer.alloc(32, 0xff)),
  };
  expectCode(
    () =>
      validateJwe(
        compact(
          JSON.stringify({
            alg: "ECDH-ES",
            enc: "A256GCM",
            epk: outOfRange,
          }),
        ),
      ),
    "JWE_ECDH_EPK_RANGE",
  );

  expectCode(
    () =>
      validateJwe(
        compact(
          JSON.stringify({
            alg: "ECDH-ES",
            enc: "A256GCM",
            epk: { ...validJwk, crv: "secp256k1" },
          }),
        ),
      ),
    "JWE_ECDH_EPK_CRV",
  );

  expectCode(
    () =>
      validateJwe(
        compact('{"alg":"ECDH-ES","enc":"A256GCM"}'),
      ),
    "JWE_ECDH_EPK_MISSING",
  );
});

test("a roughly 500-byte zip bomb is cut off by byte and ratio limits", async () => {
  const cek = Buffer.alloc(32, 0x42);
  const header = '{"alg":"dir","enc":"A256GCM","zip":"DEF"}';
  const bombPlaintext = Buffer.alloc(280_000, 0);
  const compressed = deflateRawSync(bombPlaintext, { level: 9 });
  const token = encryptGcm({
    headerText: header,
    cek,
    plaintext: compressed,
  });

  assert.ok(
    token.length <= 500,
    `expected a compact token <= 500 bytes, got ${token.length}`,
  );
  assert.ok(compressed.length < 400);

  const byteLimit = {
    maxPlaintextBytes: 64 * 1024,
    maxRatio: 10_000,
  } satisfies JwePolicy;
  const ratioLimit = {
    maxPlaintextBytes: 64 * 1024,
    maxRatio: 30,
  } satisfies JwePolicy;

  await expectRejectCode(
    () => decryptJwe(token, { cek }, byteLimit),
    "JWE_DECOMPRESSION_LIMIT",
  );
  await expectRejectCode(
    () => decryptJwe(token, { cek }, ratioLimit),
    "JWE_DECOMPRESSION_RATIO_LIMIT",
  );

  const directRaw = await expectRejectCode(
    () =>
      boundedInflate(compressed, {
        maxPlaintextBytes: 64 * 1024,
        maxRatio: 30,
      }),
    "JWE_DECOMPRESSION_RATIO_LIMIT",
  );
  assert.ok(
    Number(directRaw.details?.outputBytesAtAbort) <= 64 * 1024 + 1024,
  );

  await expectRejectCode(
    () =>
      boundedInflate(deflateSync(bombPlaintext, { level: 9 }), {
        maxPlaintextBytes: 64 * 1024,
        maxRatio: 30,
      }),
    "JWE_DECOMPRESSION_RATIO_LIMIT",
  );
});

test("small RFC 1951 DEF payloads decompress normally", async () => {
  const cek = Buffer.alloc(32, 0x51);
  const plaintext = Buffer.from("bounded deflate works");
  const compressed = deflateRawSync(plaintext);
  const token = encryptGcm({
    headerText: '{"alg":"dir","enc":"A256GCM","zip":"DEF"}',
    cek,
    plaintext: compressed,
  });

  const result = await decryptJwe(token, { cek });
  assert.equal(result.compressed, true);
  assert.deepEqual(result.plaintext, plaintext);
});

test("empty authenticated plaintext remains a valid compact JWE", async () => {
  const cek = Buffer.alloc(32, 0x5a);
  const token = encryptGcm({
    headerText: '{"alg":"dir","enc":"A256GCM"}',
    cek,
    plaintext: Buffer.alloc(0),
  });

  const result = await decryptJwe(token, { cek });
  assert.equal(result.plaintext.length, 0);
});

test("AAD is the original protected-header segment, not reserialized JSON", async () => {
  const cek = Buffer.alloc(32, 0x33);
  const headerText = '{"alg":"dir", "enc":"A256GCM"}';
  const token = encryptGcm({
    headerText,
    cek,
    plaintext: Buffer.from("authenticated"),
  });
  const parsed = parseCompactJwe(token);
  const result = await decryptJwe(token, { cek });

  assert.deepEqual(result.aad, Buffer.from(parsed.protectedHeaderSegment, "ascii"));
  assert.deepEqual(result.plaintext, Buffer.from("authenticated"));

  const equivalentHeader = b64('{"alg":"dir","enc":"A256GCM"}');
  const tampered = [
    equivalentHeader,
    parsed.encryptedKeySegment,
    parsed.ivSegment,
    parsed.ciphertextSegment,
    parsed.tagSegment,
  ].join(".");

  assert.equal(validateJwe(tampered).algorithm, "dir");
  await expectRejectCode(
    () => decryptJwe(tampered, { cek }),
    "JWE_AUTH_TAG_INVALID",
  );
});

test("valid PBES2 and ECDH-ES tokens decrypt with bounded keys", async () => {
  const password = "correct horse battery staple";
  const cek = Buffer.alloc(32, 0x77);
  const p2s = randomBytes(12);
  const p2c = 10_000;
  const alg = "PBES2-HS256+A128KW";
  const kek = pbkdf2Sync(
    password,
    Buffer.concat([Buffer.from(alg, "utf8"), p2s]),
    p2c,
    16,
    "sha256",
  );
  const pbes2Header = JSON.stringify({
    alg,
    enc: "A256GCM",
    p2s: b64(p2s),
    p2c,
  });
  const pbes2Token = encryptGcm({
    headerText: pbes2Header,
    cek,
    plaintext: Buffer.from("pbes2"),
    encryptedKey: aesKeyWrap(kek, cek),
  });
  assert.deepEqual(
    (await decryptJwe(pbes2Token, { pbes2Password: password })).plaintext,
    Buffer.from("pbes2"),
  );

  const staticPair = generateKeyPairSync("ec", {
    namedCurve: "prime256v1",
  });
  const ephemeralPair = generateKeyPairSync("ec", {
    namedCurve: "prime256v1",
  });
  const epk = ephemeralPair.publicKey.export({ format: "jwk" });
  const apu = Buffer.from("gateway");
  const apv = Buffer.from("client");
  const secret = diffieHellman({
    privateKey: staticPair.privateKey,
    publicKey: ephemeralPair.publicKey,
  });
  const ecdhCek = deriveConcatKdf(
    secret,
    "A256GCM",
    256,
    apu,
    apv,
  );
  const ecdhHeader = JSON.stringify({
    alg: "ECDH-ES",
    enc: "A256GCM",
    epk,
    apu: b64(apu),
    apv: b64(apv),
  });
  const ecdhToken = encryptGcm({
    headerText: ecdhHeader,
    cek: ecdhCek,
    plaintext: Buffer.from("ecdh"),
  });
  assert.deepEqual(
    (
      await decryptJwe(ecdhToken, {
        privateKey: staticPair.privateKey,
      })
    ).plaintext,
    Buffer.from("ecdh"),
  );
});

test("AES-CBC-HS authenticates raw AAD before CBC decryption", async () => {
  const cek = Buffer.alloc(32, 0x19);
  const token = encryptCbcHs({
    headerText: '{"alg":"dir", "enc":"A128CBC-HS256"}',
    cek,
    plaintext: Buffer.from("cbc-hs"),
  });
  assert.deepEqual(
    (await decryptJwe(token, { cek })).plaintext,
    Buffer.from("cbc-hs"),
  );

  const parsed = parseCompactJwe(token);
  const tampered = [
    b64('{"alg":"dir","enc":"A128CBC-HS256"}'),
    parsed.encryptedKeySegment,
    parsed.ivSegment,
    parsed.ciphertextSegment,
    parsed.tagSegment,
  ].join(".");
  await expectRejectCode(
    () => decryptJwe(tampered, { cek }),
    "JWE_AUTH_TAG_INVALID",
  );
});

test("RSA-OAEP-256 unwraps a CEK under the configured private key", async () => {
  const pair = generateKeyPairSync("rsa", {
    modulusLength: 2048,
  });
  const cek = randomBytes(32);
  const rsaWrapped = publicEncrypt(
    {
      key: pair.publicKey,
      padding: cryptoConstants.RSA_PKCS1_OAEP_PADDING,
      oaepHash: "sha256",
    },
    cek,
  );
  const token = encryptGcm({
    headerText: '{"alg":"RSA-OAEP-256","enc":"A256GCM"}',
    cek,
    plaintext: Buffer.from("rsa"),
    encryptedKey: rsaWrapped,
  });

  assert.deepEqual(
    (await decryptJwe(token, { privateKey: pair.privateKey })).plaintext,
    Buffer.from("rsa"),
  );

  const wrongPair = generateKeyPairSync("rsa", {
    modulusLength: 2048,
  });
  await expectRejectCode(
    () => decryptJwe(token, { privateKey: wrongPair.privateKey }),
    "JWE_KEY_UNWRAP_FAILED",
  );
});
