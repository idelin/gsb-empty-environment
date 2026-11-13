# Query response proof verification

This package is the acceptance gate a TypeScript gateway calls after it has
received an indexer's response and signed query proof. It deliberately has no
network, database, or secp256k1 dependency:

- EIP-712 signature recovery is supplied by the caller.
- The current on-chain allocation table (or an allocation lookup callback) is
supplied by the caller.
- Keccak-256 is implemented locally, so body hashes do not depend on the
gateway's crypto library.

## Quick start

```ts
import { verifyQueryResponse } from "./index.ts";

const result = await verifyQueryResponse({
  request: { body: requestBody, deployment },
  response: { body: responseBody },
  proof,
  chainHead,
  indexer: expectedIndexer,
  recoverSigner: async ({ signature, domain, types, primaryType, message }) =>
    recoverTypedDataAddress({ domain, types, primaryType, message, signature }),
  allocations: new Map([
    [
      allocationId,
      { allocationId, indexer: expectedIndexer, deployment, active: true },
    ],
  ]),
});

if (!result.ok) {
  throw new Error(`${result.code}: ${result.reason}`);
}
```

`verifyQueryProof`, `verifyResponseProof`, and `verifyIndexerResponse` are
aliases. `assertValidQueryResponse` throws `QueryResponseVerificationError`
instead of returning a failure object. `createQueryResponseVerifier` binds
gateway-wide dependencies such as `chainHead`, `recoverSigner`, and
`allocations`.

## Body hashing contract

The proof must bind the exact bodies accepted by the gateway:

- A string or byte body is hashed as its exact UTF-8 bytes. This is intended
  for the raw HTTP body.
- A parsed JSON body is serialized with `JSON.stringify` before hashing.
- `keccak256(body)` and `keccak256Bytes(bytes)` are exported as helpers.

Object property order therefore matters when a caller passes parsed objects.
Pass the raw response text when exact wire-byte binding is required.

If a `response: { body }` value is a raw JSON string, the verifier hashes the
string and separately parses a copy to inspect `errors` and `_meta`.

## Accepted response rules

All of these checks must pass:

- `proof.requestHash === keccak256(actual request body)`.
- `proof.responseHash === keccak256(actual response body)`.
- The recovered EIP-712 signer is a valid non-zero address. When the proof also
  names an allocation, that allocation must equal the recovered signer.
- The recovered signer is an allocation in the injected table with
  `active: true` (or `status: "active"` / `"open"`).
- If an allocation record names a deployment, it must equal the query
  deployment.
- If `indexer` is supplied, the allocation record must name that indexer. A
  record without an indexer is rejected in this mode.
- `proof.deployment` equals the query deployment.
- The response has no non-empty `errors` array, including under `data`.
- `data._meta.block.number` (or top-level `_meta.block.number`) exists.
- The response block is `>=` the request's `number_gte`.
- The response block is `<=` the gateway's caller-supplied `chainHead`.

`number_gte` is read from `numberGte` / `number_gte`, the request context,
`variables.number_gte`, or a numeric inline GraphQL `number_gte: 123` value.
When the query has no lower bound, block `0` is the implicit lower bound.

## Injection shapes

### Signature recovery

The default EIP-712 payload is:

```ts
{
  domain: { name: "Graph Query Response", version: "1" },
  types: {
    QueryResponse: [
      { name: "requestHash", type: "bytes32" },
      { name: "responseHash", type: "bytes32" },
      { name: "deployment", type: "bytes32" },
    ],
  },
  primaryType: "QueryResponse",
  message: { requestHash, responseHash, deployment },
}
```

The recovery callback receives this viem-compatible object plus `signature`,
`proof`, `requestHash`, `responseHash`, and `deployment`. Override
`domain`, `types`, `primaryType`, or `typedData` when the indexer signs a
different schema. The verifier does not silently substitute its own values for
the hashes it has computed.

### Allocation table

The canonical table is `Map<allocationId, AllocationRecord>` or a plain object
keyed by allocation ID. A record may use `allocationId`, `id`, or `address`
for its identifier, and `deployment`, `deploymentId`, or
`subgraphDeployment` for its deployment.

Also accepted:

- `AllocationRecord[]`
- `Set<allocationId>` (membership means active)
- deployment-scoped objects/Maps with an active allocation ID array
- `getAllocation(allocationId, deployment?, indexer?)` callbacks
- a boolean table value (`true` active, `false` closed)

The option aliases `allocationTable`, `activeAllocations`,
`allocationLookup`, and `getActiveAllocation` are accepted for gateway adapters.

## Verification results

Success returns `{ ok: true, signer, allocation, deployment, requestHash,
responseHash, blockNumber, ... }`. Failures return `{ ok: false, code, reason,
details? }` with stable codes including:

`REQUEST_HASH_MISMATCH`, `RESPONSE_HASH_MISMATCH`, `DEPLOYMENT_MISMATCH`,
`ALLOCATION_NOT_FOUND`, `ALLOCATION_NOT_ACTIVE`,
`ALLOCATION_DEPLOYMENT_MISMATCH`, `ALLOCATION_INDEXER_MISMATCH`,
`RESPONSE_HAS_ERRORS`, `BLOCK_BELOW_NUMBER_GTE`, and
`BLOCK_ABOVE_CHAIN_HEAD`.

Run the acceptance tests with:

```sh
npm test
```
