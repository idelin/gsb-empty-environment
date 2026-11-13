import assert from "node:assert/strict";
import test from "node:test";

import {
  assertValidQueryResponse,
  createQueryResponseVerifier,
  keccak256,
  QueryResponseVerificationError,
  verifyQueryResponse,
  type AllocationRecord,
  type QueryProof,
  type QueryProofRecoveryInput,
  type VerificationFailureCode,
  type HashableBody,
} from "../index.ts";

const DEPLOYMENT =
  "0x1111111111111111111111111111111111111111111111111111111111111111";
const OTHER_DEPLOYMENT =
  "0x2222222222222222222222222222222222222222222222222222222222222222";
const INDEXER = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const OTHER_INDEXER = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
const ALLOCATION = "0xcccccccccccccccccccccccccccccccccccccccc";
const OTHER_ALLOCATION = "0xdddddddddddddddddddddddddddddddddddddddd";
const STRANGER = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";

const requestBody = {
  query:
    "query Items($number_gte: Int!) { items(first: 10, where: { number_gte: $number_gte }) { id } _meta { block { number } } }",
  variables: { number_gte: 100 },
};

const responseBody = {
  data: {
    items: [{ id: "0x01" }],
    _meta: { block: { number: 150 } },
  },
};

function proofFor(
  response: unknown = responseBody,
  deployment: string = DEPLOYMENT,
  request: unknown = requestBody,
): QueryProof {
  return {
    requestHash: keccak256(request as HashableBody),
    responseHash: keccak256(response as HashableBody),
    deployment,
    signature: "0x1234",
  };
}

function activeAllocation(
  overrides: Partial<AllocationRecord> = {},
): AllocationRecord {
  return {
    allocationId: ALLOCATION,
    indexer: INDEXER,
    deployment: DEPLOYMENT,
    active: true,
    ...overrides,
  };
}

function verifierOptions(
  options: {
    request?: unknown;
    response?: unknown;
    proof?: QueryProof;
    allocations?: unknown;
    signer?: string;
    chainHead?: number;
    indexer?: string;
  } = {},
) {
  return {
    request: { body: options.request ?? requestBody, deployment: DEPLOYMENT },
    response: { body: options.response ?? responseBody },
    proof: options.proof ?? proofFor(options.response),
    chainHead: options.chainHead ?? 200,
    recoverSigner: async (_input: QueryProofRecoveryInput) =>
      options.signer ?? ALLOCATION,
    allocations: options.allocations ?? {
      [ALLOCATION.toLowerCase()]: activeAllocation(),
    },
    indexer: options.indexer,
  };
}

async function expectFailure(
  options: ReturnType<typeof verifierOptions>,
  code: VerificationFailureCode,
): Promise<void> {
  const result = await verifyQueryResponse(options);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.code, code);
  }
}

test("keccak256 matches canonical Ethereum vectors", () => {
  assert.equal(
    keccak256(""),
    "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
  );
  assert.equal(
    keccak256("abc"),
    "0x4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45",
  );
  assert.equal(
    keccak256("The quick brown fox jumps over the lazy dog"),
    "0x4d741b6f1eb29cb2a9b9911c82f56fa8d73b04959d3d9d222895df6c0b28aa15",
  );
});

test("accepts a response bound to the request and active allocation", async () => {
  const result = await verifyQueryResponse(verifierOptions());

  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(result.signer, ALLOCATION);
    assert.equal(result.deployment, DEPLOYMENT);
    assert.equal(result.blockNumber, 150);
    assert.equal(result.numberGte, 100);
    assert.equal(result.chainHead, 200);
    assert.equal(
      result.requestHash,
      keccak256(JSON.stringify(requestBody)),
    );
    assert.equal(
      result.responseHash,
      keccak256(JSON.stringify(responseBody)),
    );
  }
});

test("rejects a swapped response body", async () => {
  const swapped = {
    data: {
      items: [{ id: "0x02" }],
      _meta: { block: { number: 150 } },
    },
  };

  await expectFailure(
    verifierOptions({ response: swapped, proof: proofFor(responseBody) }),
    "RESPONSE_HASH_MISMATCH",
  );
});

test("rejects a swapped request body", async () => {
  const swappedRequest = {
    ...requestBody,
    variables: { number_gte: 120 },
  };

  await expectFailure(
    verifierOptions({
      request: swappedRequest,
      proof: proofFor(responseBody, DEPLOYMENT, requestBody),
    }),
    "REQUEST_HASH_MISMATCH",
  );
});

test("rejects a signature from a closed allocation", async () => {
  await expectFailure(
    verifierOptions({
      allocations: {
        [ALLOCATION]: activeAllocation({ active: false }),
      },
    }),
    "ALLOCATION_NOT_ACTIVE",
  );
});

test("rejects a signer that is not an active allocation", async () => {
  await expectFailure(
    verifierOptions({ signer: STRANGER }),
    "ALLOCATION_NOT_FOUND",
  );
});

test("rejects a valid proof from another deployment", async () => {
  await expectFailure(
    verifierOptions({
      proof: proofFor(responseBody, OTHER_DEPLOYMENT),
    }),
    "DEPLOYMENT_MISMATCH",
  );
});

test("rejects an active allocation belonging to another indexer", async () => {
  await expectFailure(
    verifierOptions({
      allocations: {
        [ALLOCATION]: activeAllocation({ indexer: OTHER_INDEXER }),
      },
      indexer: INDEXER,
    }),
    "ALLOCATION_INDEXER_MISMATCH",
  );
});

test("rejects a proof from another query", async () => {
  await expectFailure(
    verifierOptions({
      proof: proofFor(responseBody, DEPLOYMENT, {
        ...requestBody,
        variables: { number_gte: 101 },
      }),
    }),
    "REQUEST_HASH_MISMATCH",
  );
});

test("rejects a response claiming a block above the chain head", async () => {
  const futureResponse = {
    data: {
      items: [{ id: "0x01" }],
      _meta: { block: { number: 201 } },
    },
  };

  await expectFailure(
    verifierOptions({
      response: futureResponse,
      proof: proofFor(futureResponse),
      chainHead: 200,
    }),
    "BLOCK_ABOVE_CHAIN_HEAD",
  );
});

test("rejects a response below number_gte", async () => {
  const oldResponse = {
    data: {
      items: [],
      _meta: { block: { number: 99 } },
    },
  };

  await expectFailure(
    verifierOptions({
      response: oldResponse,
      proof: proofFor(oldResponse),
    }),
    "BLOCK_BELOW_NUMBER_GTE",
  );
});

test("rejects a response containing GraphQL errors", async () => {
  const erroredResponse = {
    data: null,
    errors: [{ message: "indexer failed to execute the query" }],
  };

  await expectFailure(
    verifierOptions({
      response: erroredResponse,
      proof: proofFor(erroredResponse),
    }),
    "RESPONSE_HAS_ERRORS",
  );
});

test("accepts inclusive block boundaries and raw JSON response bodies", async () => {
  const boundaryResponse = {
    data: {
      items: [],
      _meta: { block: { number: 200 } },
    },
  };

  const options = {
    ...verifierOptions({
      response: JSON.stringify(boundaryResponse),
      proof: proofFor(JSON.stringify(boundaryResponse)),
      chainHead: 200,
    }),
    numberGte: 200,
    indexer: INDEXER,
  };

  const result = await verifyQueryResponse(options);
  assert.equal(result.ok, true);
});

test("passes the verified hashes and deployment to EIP-712 recovery", async () => {
  let recoveredInput: QueryProofRecoveryInput | undefined;
  const result = await verifyQueryResponse({
    request: { body: requestBody, deployment: DEPLOYMENT },
    response: { body: new TextEncoder().encode(JSON.stringify(responseBody)) },
    proof: proofFor(),
    chainHead: 200,
    recoverSigner: (input) => {
      recoveredInput = input;
      return ALLOCATION;
    },
    allocations: { [ALLOCATION]: activeAllocation() },
  });

  assert.equal(result.ok, true);
  assert.ok(recoveredInput);
  assert.equal(recoveredInput.signature, "0x1234");
  assert.equal(recoveredInput.deployment, DEPLOYMENT);
  assert.equal(
    recoveredInput.message.requestHash,
    keccak256(requestBody),
  );
  assert.equal(
    recoveredInput.message.responseHash,
    keccak256(JSON.stringify(responseBody)),
  );
});

test("supports deployment-scoped allocation tables and async recovery", async () => {
  const result = await verifyQueryResponse({
    request: { body: requestBody, deployment: DEPLOYMENT },
    response: { body: responseBody },
    proof: proofFor(),
    chainHead: 200,
    recoverSigner: async () => Promise.resolve(ALLOCATION),
    allocations: {
      [DEPLOYMENT]: [
        {
          allocationId: ALLOCATION,
          indexer: INDEXER,
          active: true,
        },
      ],
    },
    indexer: INDEXER,
  });

  assert.equal(result.ok, true);
});

test("flat gateway options are accepted", async () => {
  const result = await verifyQueryResponse({
    requestBody: JSON.stringify(requestBody),
    responseBody: JSON.stringify(responseBody),
    queryDeployment: DEPLOYMENT,
    numberGte: 100,
    proof: proofFor(),
    chainHead: 200,
    recoverSigner: () => ALLOCATION,
    allocations: new Map([[ALLOCATION, activeAllocation()]]),
  });

  assert.equal(result.ok, true);
});

test("factory binds dependencies while allowing per-response options", async () => {
  const verify = createQueryResponseVerifier({
    chainHead: 200,
    recoverSigner: () => ALLOCATION,
    allocations: { [ALLOCATION]: activeAllocation() },
    indexer: INDEXER,
  });

  const result = await verify({
    request: { body: requestBody, deployment: DEPLOYMENT },
    response: { body: responseBody },
    proof: proofFor(),
  });

  assert.equal(result.ok, true);
});

test("assertValidQueryResponse throws with the verification result", async () => {
  const failing = verifierOptions({
    proof: proofFor(responseBody, OTHER_DEPLOYMENT),
  });

  await assert.rejects(
    () => assertValidQueryResponse(failing),
    (error: unknown) => {
      assert.ok(error instanceof QueryResponseVerificationError);
      assert.equal(error.result.code, "DEPLOYMENT_MISMATCH");
      return true;
    },
  );
});
