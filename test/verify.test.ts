import assert from "node:assert/strict";
import { createHmac, timingSafeEqual } from "node:crypto";
import test from "node:test";

import {
  bytesToHex,
  hashDomain,
  hashBody,
  InMemoryAllocationTable,
  keccak256Hex,
  responseProofDigest,
  verifyResponseProof,
  type BodyInput,
  type ResponseProof,
  type ResponseProofRecoveryInput,
  type VerifierDependencies,
} from "../src/index.ts";

const INDEXER = "0x1111111111111111111111111111111111111111";
const ACTIVE_ALLOCATION = "0x2222222222222222222222222222222222222222";
const CLOSED_ALLOCATION = "0x3333333333333333333333333333333333333333";
const DEPLOYMENT_A = `0x${"aa".repeat(32)}`;
const DEPLOYMENT_B = `0x${"bb".repeat(32)}`;
const SECRET = "gateway-proof-test-secret";

function macFor(signer: string, digest: string): string {
  return createHmac("sha256", `${SECRET}:${signer.toLowerCase()}`)
    .update(digest)
    .digest("hex");
}

function fakeSign(signer: string, digest: string): string {
  return `0x${signer.slice(2).toLowerCase()}${macFor(signer, digest)}`;
}

function fakeRecover({
  digest,
  signature,
}: ResponseProofRecoveryInput): string {
  const payload = signature.slice(2);
  if (payload.length !== 104) {
    throw new Error("invalid test signature length");
  }
  const signer = `0x${payload.slice(0, 40)}`;
  const suppliedMac = payload.slice(40);
  const expectedMac = macFor(signer, digest);
  const supplied = Buffer.from(suppliedMac, "hex");
  const expected = Buffer.from(expectedMac, "hex");
  if (
    supplied.length !== expected.length ||
    !timingSafeEqual(supplied, expected)
  ) {
    throw new Error("signature does not match digest");
  }
  return signer;
}

const dependencies: VerifierDependencies = {
  recoverSigner: fakeRecover,
  allocationTable: new InMemoryAllocationTable([
    {
      allocationId: ACTIVE_ALLOCATION,
      indexer: INDEXER,
      deployment: DEPLOYMENT_A,
    },
    {
      allocationId: CLOSED_ALLOCATION,
      indexer: INDEXER,
      deployment: DEPLOYMENT_A,
      active: false,
      closed: true,
      closedAtBlock: 150,
    },
  ]),
};

function responseBody(blockNumber: number, extra: Record<string, unknown> = {}): string {
  return JSON.stringify({
    data: {
      _meta: { block: { number: blockNumber } },
      pairs: [{ id: "pair-1" }],
    },
    ...extra,
  });
}

function requestBody(numberGte = 100): string {
  return JSON.stringify({
    query:
      "query Pair($block: Int!) { pairs(block: { number_gte: $block }) { id } }",
    variables: { block: numberGte },
  });
}

function proofFor(options: {
  request: BodyInput;
  response: BodyInput;
  signer: string;
  deployment: string;
}): ResponseProof {
  const requestHash = hashBody(options.request);
  const responseHash = hashBody(options.response);
  const message = {
    requestHash,
    responseHash,
    deployment: options.deployment,
  };
  return {
    ...message,
    signature: fakeSign(options.signer, responseProofDigest(message)),
  };
}

function input(
  proof: ResponseProof,
  response: BodyInput,
  options: {
    request?: BodyInput;
    deployment?: string;
    block?: number;
    chainHead?: number;
  } = {},
) {
  return {
    requestBody: options.request ?? requestBody(),
    responseBody: response,
    proof,
    query: {
      indexer: INDEXER,
      deployment: options.deployment ?? DEPLOYMENT_A,
      numberGte: options.block ?? 100,
    },
    chainHead: options.chainHead ?? 200,
  };
}

test("keccak256 matches known vectors", () => {
  assert.equal(
    keccak256Hex(new Uint8Array()),
    "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
  );
  assert.equal(
    keccak256Hex(new TextEncoder().encode("abc")),
    "0x4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45",
  );
  assert.equal(
    keccak256Hex(new TextEncoder().encode("hello")),
    "0x1c8aff950685c2ed4bc3174f3472287b56d9517b9c948127319a09a7a36deac8",
  );
});

test("EIP-712 domain hashing matches the canonical Ether Mail example", () => {
  const separator = hashDomain({
    name: "Ether Mail",
    version: "1",
    chainId: 1,
    verifyingContract: "0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC",
  });
  assert.equal(
    bytesToHex(separator),
    "0xf2cee375fa42b42143804025fc449deafd50cc031ca257e0b194a650a912090f",
  );
});

test("accepts a proof bound to the request, response, deployment and active allocation", async () => {
  const response = responseBody(150);
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(result.signer, ACTIVE_ALLOCATION);
    assert.equal(result.allocationId, ACTIVE_ALLOCATION);
    assert.equal(result.blockNumber, 150n);
  }
});

test("rejects a proof when the response body is swapped", async () => {
  const originalResponse = responseBody(150);
  const proof = proofFor({
    request: requestBody(),
    response: originalResponse,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(
    input(proof, responseBody(151)),
    dependencies,
  );

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "RESPONSE_HASH_MISMATCH");
  }
});

test("rejects a proof signed by a closed allocation", async () => {
  const response = responseBody(150);
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: CLOSED_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "SIGNER_NOT_ACTIVE_ALLOCATION");
  }
});

test("rejects a closed allocation when the injected table returns full records", async () => {
  const response = responseBody(150);
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: CLOSED_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });
  const fullTable: VerifierDependencies = {
    recoverSigner: fakeRecover,
    allocationTable: {
      getAllocations: () => [
        {
          allocationId: ACTIVE_ALLOCATION,
          indexer: INDEXER,
          deployment: DEPLOYMENT_A,
        },
        {
          allocationId: CLOSED_ALLOCATION,
          indexer: INDEXER,
          deployment: DEPLOYMENT_A,
          closedAtBlock: 150,
        },
      ],
    },
  };

  const result = await verifyResponseProof(input(proof, response), fullTable);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "SIGNER_NOT_ACTIVE_ALLOCATION");
  }
});

test("rejects a proof carrying another deployment id", async () => {
  const response = responseBody(150);
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_B,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "DEPLOYMENT_MISMATCH");
  }
});

test("rejects a response claiming a block above the caller chain head", async () => {
  const response = responseBody(201);
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "BLOCK_ABOVE_CHAIN_HEAD");
  }
});

test("rejects a response containing errors even when correctly signed", async () => {
  const response = responseBody(150, {
    errors: [{ message: "resolver failed" }],
  });
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "RESPONSE_HAS_ERRORS");
  }
});

test("rejects a response that carries an empty errors member", async () => {
  const response = responseBody(150, { errors: [] });
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "RESPONSE_HAS_ERRORS");
  }
});

test("rejects a proof taken from a different query", async () => {
  const response = responseBody(150);
  const otherRequest = requestBody(101);
  const proof = proofFor({
    request: otherRequest,
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "REQUEST_HASH_MISMATCH");
  }
});

test("rejects a block below the query number_gte", async () => {
  const response = responseBody(99);
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(input(proof, response), dependencies);

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "BLOCK_BELOW_MINIMUM");
  }
});

test("rejects a signature recovered over a different digest", async () => {
  const response = responseBody(150);
  const proof = proofFor({
    request: requestBody(),
    response,
    signer: ACTIVE_ALLOCATION,
    deployment: DEPLOYMENT_A,
  });

  const result = await verifyResponseProof(
    {
      ...input(proof, response),
      proof: { ...proof, signature: fakeSign(ACTIVE_ALLOCATION, "0xdeadbeef") },
    },
    dependencies,
  );

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, "SIGNATURE_RECOVERY_FAILED");
  }
});
