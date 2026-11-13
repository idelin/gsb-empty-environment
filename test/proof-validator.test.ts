import assert from "node:assert/strict";
import {
  constants,
  createSign,
  generateKeyPairSync,
  sign as signMessage,
  type KeyObject,
} from "node:crypto";
import test from "node:test";

import {
  NonceStore,
  ProofValidator,
  PROOF_JWT_TYP,
} from "../index.ts";

const ISSUER = "https://issuer.example.com";

function encodeJson(value: unknown): string {
  return Buffer.from(JSON.stringify(value)).toString("base64url");
}

function publicJwk(key: KeyObject): Record<string, unknown> {
  return key.export({ format: "jwk" }) as Record<string, unknown>;
}

function privateJwk(key: KeyObject): Record<string, unknown> {
  return key.export({ format: "jwk" }) as Record<string, unknown>;
}

function signProof(
  privateKey: KeyObject,
  header: Record<string, unknown>,
  payload: Record<string, unknown>,
): string {
  const encodedHeader = encodeJson(header);
  const encodedPayload = encodeJson(payload);
  const signingInput = `${encodedHeader}.${encodedPayload}`;

  const signer = createSign("SHA256");
  signer.update(signingInput);
  const signature = signer.sign({
    key: privateKey,
    dsaEncoding: "ieee-p1363",
  });

  return `${signingInput}.${Buffer.from(signature).toString("base64url")}`;
}

function setup() {
  const clock = { now: 1_700_000_000_000 };
  const store = new NonceStore({
    now: () => clock.now,
    nonceTtlSeconds: 300,
  });
  const validator = new ProofValidator({
    issuer: ISSUER,
    nonceStore: store,
  });
  const { publicKey, privateKey } = generateKeyPairSync("ec", {
    namedCurve: "P-256",
  });
  const jwk = publicJwk(publicKey);

  const makeProof = (
    nonce: string,
    options: {
      header?: Record<string, unknown>;
      payload?: Record<string, unknown>;
      signingKey?: KeyObject;
    } = {},
  ) =>
    signProof(
      options.signingKey ?? privateKey,
      options.header ?? {
        typ: PROOF_JWT_TYP,
        alg: "ES256",
        jwk,
      },
      options.payload ?? {
        aud: ISSUER,
        nonce,
        iat: Math.floor(clock.now / 1000),
      },
    );

  return { clock, store, validator, makeProof, privateKey, jwk };
}

test("accepts a fresh proof and consumes the nonce", () => {
  const { validator, makeProof } = setup();
  const nonce = validator.issueNonce();

  const result = validator.validate(makeProof(nonce));

  assert.equal(result.valid, true);
});

test("rejects replay of an already consumed nonce", () => {
  const { validator, makeProof } = setup();
  const nonce = validator.issueNonce();
  const proof = makeProof(nonce);

  assert.equal(validator.validate(proof).valid, true);

  const replay = validator.validate(proof);
  assert.equal(replay.valid, false);
  assert.equal(replay.valid ? undefined : replay.code, "NONCE_ALREADY_USED");
});

test("rejects a proof for another issuer", () => {
  const { validator, makeProof } = setup();
  const nonce = validator.issueNonce();

  const result = validator.validate(
    makeProof(nonce, {
      payload: {
        aud: "https://other-issuer.example.com",
        nonce,
        iat: 1_700_000_000,
      },
    }),
  );

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "INVALID_AUDIENCE");
});

test("rejects a nonce that was not issued by this service", () => {
  const { validator, makeProof } = setup();

  const result = validator.validate(makeProof("not-issued-here"));

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "UNKNOWN_NONCE");
});

test("rejects an expired nonce", () => {
  const { clock, validator, makeProof } = setup();
  const nonce = validator.issueNonce();
  clock.now += 301_000;

  const result = validator.validate(
    makeProof(nonce, {
      payload: {
        aud: ISSUER,
        nonce,
        iat: Math.floor(clock.now / 1000),
      },
    }),
  );

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "NONCE_EXPIRED");
});

test("rejects iat before the nonce issuance time", () => {
  const { validator, makeProof } = setup();
  const nonce = validator.issueNonce();

  const result = validator.validate(
    makeProof(nonce, {
      payload: {
        aud: ISSUER,
        nonce,
        iat: 1_699_999_999,
      },
    }),
  );

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "IAT_BEFORE_NONCE");
});

test("rejects iat more than 60 seconds in the future", () => {
  const { validator, makeProof } = setup();
  const nonce = validator.issueNonce();

  const result = validator.validate(
    makeProof(nonce, {
      payload: {
        aud: ISSUER,
        nonce,
        iat: 1_700_000_061,
      },
    }),
  );

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "IAT_TOO_FAR_IN_FUTURE");
});

test("accepts iat exactly 60 seconds in the future", () => {
  const { validator, makeProof } = setup();
  const nonce = validator.issueNonce();

  const result = validator.validate(
    makeProof(nonce, {
      payload: {
        aud: ISSUER,
        nonce,
        iat: 1_700_000_060,
      },
    }),
  );

  assert.equal(result.valid, true);
});

test("accepts the nonce issuance second when issued mid-second", () => {
  const clock = { now: 1_700_000_000_500 };
  const store = new NonceStore({ now: () => clock.now });
  const validator = new ProofValidator({ issuer: ISSUER, nonceStore: store });
  const nonce = validator.issueNonce();
  const { publicKey, privateKey } = generateKeyPairSync("ec", {
    namedCurve: "P-256",
  });

  const proof = signProof(
    privateKey,
    { typ: PROOF_JWT_TYP, alg: "ES256", jwk: publicJwk(publicKey) },
    { aud: ISSUER, nonce, iat: Math.floor(clock.now / 1000) },
  );

  assert.equal(validator.validate(proof).valid, true);
});

test("requires the OpenID4VCI proof JWT typ", () => {
  const { validator, makeProof } = setup();
  const nonce = validator.issueNonce();

  const result = validator.validate(
    makeProof(nonce, {
      header: {
        typ: "JWT",
        alg: "ES256",
        jwk: publicJwk(generateKeyPairSync("ec", { namedCurve: "P-256" }).publicKey),
      },
    }),
  );

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "INVALID_TYP");
});

test("rejects a jwk header that contains private key member d", () => {
  const { validator, makeProof, privateKey } = setup();
  const nonce = validator.issueNonce();

  const result = validator.validate(
    makeProof(nonce, {
      header: {
        typ: PROOF_JWT_TYP,
        alg: "ES256",
        jwk: privateJwk(privateKey),
      },
    }),
  );

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "PRIVATE_JWK");
});

test("rejects a signature made with a different key", () => {
  const { validator, makeProof, jwk } = setup();
  const nonce = validator.issueNonce();
  const otherKey = generateKeyPairSync("ec", { namedCurve: "P-256" }).privateKey;

  const result = validator.validate(
    makeProof(nonce, {
      header: {
        typ: PROOF_JWT_TYP,
        alg: "ES256",
        jwk,
      },
      signingKey: otherKey,
    }),
  );

  assert.equal(result.valid, false);
  assert.equal(result.valid ? undefined : result.code, "INVALID_SIGNATURE");
});

test("accepts RS256, PS256, and EdDSA proofs", () => {
  const issuer = ISSUER;
  const algorithms = [
    {
      alg: "RS256",
      keys: generateKeyPairSync("rsa", { modulusLength: 2048 }),
      sign: (input: Buffer, key: KeyObject) => {
        const signer = createSign("SHA256");
        signer.update(input);
        return signer.sign(key);
      },
    },
    {
      alg: "PS256",
      keys: generateKeyPairSync("rsa", { modulusLength: 2048 }),
      sign: (input: Buffer, key: KeyObject) => {
        const signer = createSign("SHA256");
        signer.update(input);
        return signer.sign({
          key,
          padding: constants.RSA_PKCS1_PSS_PADDING,
          saltLength: constants.RSA_PSS_SALTLEN_DIGEST,
        });
      },
    },
    {
      alg: "EdDSA",
      keys: generateKeyPairSync("ed25519"),
      sign: (input: Buffer, key: KeyObject) => signMessage(null, input, key),
    },
  ];

  for (const algorithm of algorithms) {
    const clock = { now: 1_700_000_000_000 };
    const store = new NonceStore({ now: () => clock.now });
    const validator = new ProofValidator({ issuer, nonceStore: store });
    const nonce = validator.issueNonce();
    const jwk = publicJwk(algorithm.keys.publicKey);
    const encodedHeader = encodeJson({
      typ: PROOF_JWT_TYP,
      alg: algorithm.alg,
      jwk,
    });
    const encodedPayload = encodeJson({
      aud: issuer,
      nonce,
      iat: Math.floor(clock.now / 1000),
    });
    const signingInput = `${encodedHeader}.${encodedPayload}`;
    const signature = algorithm.sign(
      Buffer.from(signingInput, "utf8"),
      algorithm.keys.privateKey,
    );
    const proof = `${signingInput}.${Buffer.from(signature).toString("base64url")}`;

    const result = validator.validate(proof);
    assert.equal(result.valid, true, `${algorithm.alg} proof should validate`);
  }
});
