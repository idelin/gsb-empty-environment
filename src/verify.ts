import {
  keccak256,
  type HashableBody,
} from "./keccak.ts";
import type {
  AllocationLookup,
  AllocationRecord,
  AllocationTable,
  AllocationValue,
  Eip712Field,
  QueryProof,
  QueryProofRecoveryInput,
  QueryProofTypedData,
  QueryRequestContext,
  RecoverSigner,
  VerificationFailure,
  VerificationFailureCode,
  VerificationResult,
  VerificationSuccess,
  VerifyQueryResponseOptions,
} from "./types.ts";

const ADDRESS_PATTERN = /^0x[0-9a-fA-F]{40}$/;
const HEX_PATTERN = /^0x[0-9a-fA-F]+$/;

const DEFAULT_DOMAIN: Record<string, unknown> = {
  name: "Graph Query Response",
  version: "1",
};

const DEFAULT_TYPES: Record<string, readonly Eip712Field[]> = {
  QueryResponse: [
    { name: "requestHash", type: "bytes32" },
    { name: "responseHash", type: "bytes32" },
    { name: "deployment", type: "bytes32" },
  ],
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function normalizeHash(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  const prefixed = trimmed.startsWith("0x") ? trimmed : `0x${trimmed}`;
  if (!/^0x[0-9a-fA-F]{64}$/.test(prefixed)) return null;
  return prefixed.toLowerCase();
}

function normalizeAddress(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  if (!ADDRESS_PATTERN.test(trimmed)) return null;
  return trimmed.toLowerCase();
}

function sameIdentifier(left: string, right: string): boolean {
  const a = left.trim();
  const b = right.trim();
  if (HEX_PATTERN.test(a) && HEX_PATTERN.test(b)) {
    return a.toLowerCase() === b.toLowerCase();
  }
  return a === b;
}

function normalizeDeployment(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  if (trimmed.length === 0) return null;
  return HEX_PATTERN.test(trimmed) ? trimmed.toLowerCase() : trimmed;
}

function parseInteger(value: unknown): bigint | null {
  if (typeof value === "bigint") {
    return value >= 0n ? value : null;
  }

  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || value < 0) return null;
    return BigInt(value);
  }

  if (typeof value === "string") {
    const trimmed = value.trim();
    if (trimmed.length === 0) return null;
    try {
      if (/^0x[0-9a-fA-F]+$/.test(trimmed)) {
        const parsed = BigInt(trimmed);
        return parsed >= 0n ? parsed : null;
      }
      if (/^[0-9]+$/.test(trimmed)) {
        return BigInt(trimmed);
      }
    } catch {
      return null;
    }
  }

  return null;
}

function toSafeNumber(value: bigint): number | null {
  if (value > BigInt(Number.MAX_SAFE_INTEGER)) return null;
  const number = Number(value);
  if (!Number.isSafeInteger(number)) return null;
  return number;
}

function firstString(
  value: Record<string, unknown>,
  names: readonly string[],
): string | null {
  for (const name of names) {
    const candidate = value[name];
    if (typeof candidate === "string" && candidate.trim().length > 0) {
      return candidate;
    }
  }
  return null;
}

function failure(
  code: VerificationFailureCode,
  reason: string,
  details?: Record<string, unknown>,
): VerificationFailure {
  return {
    ok: false,
    valid: false,
    isValid: false,
    accepted: false,
    code,
    reason,
    error: { code, message: reason },
    details,
  };
}

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function bodyFromContext(
  value: unknown,
  bodyKeys: readonly string[],
): HashableBody | undefined {
  if (typeof value === "string" || value instanceof Uint8Array || value instanceof ArrayBuffer) {
    return value;
  }
  if (value !== null && typeof value === "object") {
    for (const key of bodyKeys) {
      if (hasOwn(value as object, key)) {
        const body = (value as Record<string, unknown>)[key];
        if (
          typeof body === "string" ||
          body instanceof Uint8Array ||
          body instanceof ArrayBuffer ||
          (body !== null && typeof body === "object")
        ) {
          return body as HashableBody;
        }
      }
    }
    if (!Array.isArray(value)) {
    return value as HashableBody;
    }
  }
  return undefined;
}

function requestBodyFromOptions(options: VerifyQueryResponseOptions): HashableBody | undefined {
  return bodyFromContext(
    options.request ?? options.query ?? options.requestBody ?? options.queryBody,
    ["body", "rawBody", "requestBody"],
  );
}

function responseBodyFromOptions(options: VerifyQueryResponseOptions): HashableBody | undefined {
  return bodyFromContext(options.response ?? options.responseBody, [
    "body",
    "rawBody",
    "responseBody",
  ]);
}

function requestContextFromOptions(options: VerifyQueryResponseOptions): QueryRequestContext | undefined {
  const request = options.request ?? options.query;
  if (
    request !== undefined &&
    request !== null &&
    typeof request === "object" &&
    !Array.isArray(request) &&
    (hasOwn(request, "body") ||
      hasOwn(request, "deployment") ||
      hasOwn(request, "deploymentId") ||
      hasOwn(request, "numberGte") ||
      hasOwn(request, "number_gte"))
  ) {
    return request as QueryRequestContext;
  }
  return undefined;
}

function findKeyDeep(
  value: unknown,
  keys: readonly string[],
  maxDepth = 8,
): unknown {
  if (maxDepth < 0 || value === null || typeof value !== "object") {
    return undefined;
  }

  if (!Array.isArray(value)) {
    for (const key of keys) {
      if (hasOwn(value, key)) {
        const candidate = (value as Record<string, unknown>)[key];
        if (candidate !== undefined && candidate !== null) return candidate;
      }
    }
  }

  const children = Array.isArray(value) ? value : Object.values(value);
  for (const child of children) {
    const found = findKeyDeep(child, keys, maxDepth - 1);
    if (found !== undefined) return found;
  }

  return undefined;
}

function numberGteFromOptions(
  options: VerifyQueryResponseOptions,
  requestBody: HashableBody | undefined,
): unknown {
  if (options.numberGte !== undefined) return options.numberGte;
  if (options.number_gte !== undefined) return options.number_gte;

  const requestContext = requestContextFromOptions(options);
  if (requestContext?.numberGte !== undefined) return requestContext.numberGte;
  if (requestContext?.number_gte !== undefined) return requestContext.number_gte;

  if (requestBody !== undefined && typeof requestBody === "object" && !(requestBody instanceof Uint8Array) && !(requestBody instanceof ArrayBuffer)) {
    const found = findKeyDeep(requestBody, ["number_gte", "numberGte"]);
    if (found !== undefined) return found;
  }

  if (typeof requestBody === "string") {
    const match = requestBody.match(/\bnumber_gte\s*:\s*"?([0-9]+)"?/);
    if (match) return match[1];
  }

  return undefined;
}

function deploymentFromOptions(
  options: VerifyQueryResponseOptions,
  requestBody: HashableBody | undefined,
): unknown {
  const requestContext = requestContextFromOptions(options);
  const candidates = [
    options.queryDeployment,
    options.deployment,
    options.deploymentId,
    requestContext?.deployment,
    requestContext?.deploymentId,
  ];

  for (const candidate of candidates) {
    if (typeof candidate === "string" && candidate.trim().length > 0) {
      return candidate;
    }
  }

  if (requestBody !== undefined && typeof requestBody === "object" && !(requestBody instanceof Uint8Array) && !(requestBody instanceof ArrayBuffer)) {
    return findKeyDeep(requestBody, ["deployment", "deploymentId", "subgraphDeployment"]);
  }

  return undefined;
}

function indexerFromOptions(
  options: VerifyQueryResponseOptions,
  requestBody: HashableBody | undefined,
): string | undefined {
  const requestContext = requestContextFromOptions(options);
  const candidates = [
    options.indexer,
    options.expectedIndexer,
    options.indexerId,
    requestContext?.indexer,
    requestContext?.indexerId,
  ];

  for (const candidate of candidates) {
    if (typeof candidate === "string" && candidate.trim().length > 0) {
      return candidate;
    }
  }

  if (
    requestBody !== undefined &&
    typeof requestBody === "object" &&
    !(requestBody instanceof Uint8Array) &&
    !(requestBody instanceof ArrayBuffer)
  ) {
    return firstString(requestBody, ["indexer", "indexerId"]) ?? undefined;
  }

  return undefined;
}

function parseJsonBody(body: HashableBody): Record<string, unknown> | null {
  if (
    typeof body === "string" ||
    body instanceof Uint8Array ||
    body instanceof ArrayBuffer
  ) {
    try {
      const text =
        typeof body === "string"
          ? body
          : new TextDecoder().decode(
              body instanceof ArrayBuffer ? new Uint8Array(body) : body,
            );
      const parsed: unknown = JSON.parse(text);
      return isRecord(parsed) ? parsed : null;
    } catch {
      return null;
    }
  }
  if (isRecord(body)) return body;
  return null;
}

function responseHasErrors(response: Record<string, unknown>): boolean {
  const roots = [response];
  if (isRecord(response.data)) roots.push(response.data);

  for (const root of roots) {
    if (!hasOwn(root, "errors")) continue;
    const errors = root.errors;
    if (errors === null || errors === undefined) continue;
    if (Array.isArray(errors)) {
      if (errors.length > 0) return true;
      continue;
    }
    return true;
  }
  return false;
}

function metaBlockNumber(response: Record<string, unknown>): bigint | null {
  const roots: Record<string, unknown>[] = [];
  if (isRecord(response.data)) roots.push(response.data);
  roots.push(response);

  for (const root of roots) {
    for (const metaKey of ["_meta", "meta"]) {
      const meta = root[metaKey];
      if (!isRecord(meta)) continue;

      const block = meta.block;
      if (isRecord(block)) {
        const number = block.number ?? block.blockNumber;
        if (number !== undefined && number !== null) {
          return parseInteger(number);
        }
      }

      const direct = meta.number ?? meta.blockNumber ?? block;
      if (
        typeof direct === "bigint" ||
        typeof direct === "number" ||
        typeof direct === "string"
      ) {
        return parseInteger(direct);
      }
    }
  }

  return null;
}

function buildDefaultTypedData(
  proof: QueryProof,
  requestHash: string,
  responseHash: string,
  deployment: string,
  options: VerifyQueryResponseOptions,
): QueryProofTypedData {
  if (typeof options.typedData === "function") {
    return options.typedData(proof);
  }
  if (options.typedData !== undefined) {
    return options.typedData;
  }

  return {
    domain: options.domain ?? DEFAULT_DOMAIN,
    types: options.types ?? DEFAULT_TYPES,
    primaryType: options.primaryType ?? "QueryResponse",
    message: {
      requestHash,
      responseHash,
      deployment,
    },
  };
}

function allocationIdentifier(record: AllocationRecord): string | null {
  for (const key of [
    "allocationId",
    "allocation",
    "id",
    "address",
  ] as const) {
    const value = record[key];
    if (typeof value === "string" && value.trim().length > 0) return value;
  }
  return null;
}

function allocationIndexer(record: AllocationRecord): string | null {
  return firstString(record, ["indexer", "indexerId"]);
}

function allocationDeployment(record: AllocationRecord): string | null {
  return firstString(record, [
    "deployment",
    "deploymentId",
    "subgraphDeployment",
    "subgraphDeploymentID",
    "subgraphDeploymentId",
  ]);
}

function recordActiveState(record: AllocationRecord): boolean {
  if (record.closed === true || record.isClosed === true) return false;

  for (const key of ["active", "isActive"] as const) {
    if (!hasOwn(record, key)) continue;
    const state = record[key];
    if (typeof state === "boolean") return state;
    if (typeof state === "number") return state === 1;
    if (typeof state === "string") {
      const normalized = state.toLowerCase();
      return (
        normalized === "true" ||
        normalized === "1" ||
        normalized === "active" ||
        normalized === "open"
      );
    }
    return false;
  }

  if (typeof record.status === "string") {
    const status = record.status.toLowerCase();
    return status === "active" || status === "open";
  }

  if (
    record.closedAt !== undefined &&
    record.closedAt !== null &&
    record.closedAt !== false
  ) {
    return false;
  }

  return false;
}

function asAllocationRecord(
  value: unknown,
  allocationId: string,
): AllocationRecord | null {
  if (value === undefined || value === null) return null;

  if (typeof value === "boolean") {
    return { allocationId, active: value };
  }

  if (typeof value === "string") {
    if (value.trim().length === 0) return null;
    return {
      allocationId,
      deployment: value,
      active: true,
    };
  }

  if (isRecord(value)) {
    const identified = allocationIdentifier(value);
    if (
      identified !== null &&
      !sameIdentifier(identified, allocationId)
    ) {
      return null;
    }
    return { allocationId, ...value } as AllocationRecord;
  }

  return null;
}

function valueAt(
  table: AllocationTable,
  key: string,
): { found: boolean; value: unknown } {
  if (table instanceof Map) {
    if (table.has(key)) {
      return { found: true, value: table.get(key) };
    }
    const lower = key.toLowerCase();
    if (table.has(lower)) {
      return { found: true, value: table.get(lower) };
    }
    for (const [candidate, value] of table) {
      if (typeof candidate === "string" && sameIdentifier(candidate, key)) {
        return { found: true, value };
      }
    }
    return { found: false, value: undefined };
  }

  if (table instanceof Set) {
    if (table.has(key)) return { found: true, value: true };
    for (const candidate of table) {
      if (typeof candidate === "string" && sameIdentifier(candidate, key)) {
        return { found: true, value: true };
      }
    }
    return { found: false, value: undefined };
  }

  if (Array.isArray(table)) {
    for (const entry of table) {
      if (typeof entry === "string") {
        if (sameIdentifier(entry, key)) return { found: true, value: true };
        continue;
      }
      if (isRecord(entry)) {
        const identified = allocationIdentifier(entry);
        if (identified !== null && sameIdentifier(identified, key)) {
          return { found: true, value: entry };
        }
      }
    }
    return { found: false, value: undefined };
  }

  const recordTable = table as Readonly<Record<string, AllocationValue>>;
  if (hasOwn(recordTable, key)) {
    return { found: true, value: recordTable[key] };
  }
  const lower = key.toLowerCase();
  if (hasOwn(recordTable, lower)) {
    return { found: true, value: recordTable[lower] };
  }
  for (const [candidate, value] of Object.entries(recordTable)) {
    if (sameIdentifier(candidate, key)) return { found: true, value };
  }

  return { found: false, value: undefined };
}

function findInDeploymentScope(
  table: AllocationTable,
  deployment: string,
  allocationId: string,
): { found: boolean; value: unknown } {
  if (Array.isArray(table)) return { found: false, value: undefined };

  let scoped: unknown;
  if (table instanceof Map) {
    scoped = table.get(deployment);
    if (scoped === undefined) {
      for (const [candidate, value] of table) {
        if (typeof candidate === "string" && sameIdentifier(candidate, deployment)) {
          scoped = value;
          break;
        }
      }
    }
  } else if (
    !(table instanceof Set) &&
    hasOwn(table as object, deployment)
  ) {
    scoped = (table as Readonly<Record<string, AllocationValue>>)[deployment];
  } else if (!(table instanceof Set)) {
    for (const [candidate, value] of Object.entries(
      table as Readonly<Record<string, AllocationValue>>,
    )) {
      if (sameIdentifier(candidate, deployment)) {
        scoped = value;
        break;
      }
    }
  }

  if (scoped === undefined || scoped === null) {
    return { found: false, value: undefined };
  }

  if (Array.isArray(scoped)) {
    for (const entry of scoped) {
      if (typeof entry === "string" && sameIdentifier(entry, allocationId)) {
        return { found: true, value: { allocationId, deployment, active: true } };
      }
      if (isRecord(entry)) {
        const identified = allocationIdentifier(entry);
        if (identified !== null && sameIdentifier(identified, allocationId)) {
          return { found: true, value: entry };
        }
      }
    }
    return { found: false, value: undefined };
  }

  if (isRecord(scoped)) {
    const found = valueAt(scoped as unknown as AllocationTable, allocationId);
    if (!found.found) return found;
    if (isRecord(found.value)) {
      const record = { ...found.value } as AllocationRecord;
      if (allocationDeployment(record) === null) {
        record.deployment = deployment;
      }
      return { found: true, value: record };
    }
    if (typeof found.value === "boolean") {
      return {
        found: true,
        value: { allocationId, deployment, active: found.value },
      };
    }
    return found;
  }

  if (typeof scoped === "boolean") {
    return {
      found: true,
      value: { allocationId, deployment, active: scoped },
    };
  }

  return { found: false, value: undefined };
}

async function lookupAllocation(
  table: AllocationTable | undefined,
  lookup: AllocationLookup | undefined,
  allocationId: string,
  deployment: string,
  indexer: string | undefined,
): Promise<AllocationRecord | null> {
  let raw: unknown;
  let found = false;

  if (lookup !== undefined) {
    raw = await lookup(allocationId, deployment, indexer);
    found = raw !== undefined;
  }

  if (!found && table !== undefined) {
    const direct = valueAt(table, allocationId);
    if (direct.found) {
      found = true;
      raw = direct.value;
    } else {
      const scoped = findInDeploymentScope(table, deployment, allocationId);
      if (scoped.found) {
        found = true;
        raw = scoped.value;
      }
    }
  }

  if (!found) return null;

  const record = asAllocationRecord(raw, allocationId);
  if (record === null) {
    if (raw === true) return { allocationId, active: true };
    return null;
  }
  if (!hasOwn(record, "active") && typeof raw === "boolean") {
    record.active = raw;
  }
  return record;
}

async function resolveOption<T>(
  value: T | (() => MaybePromise<T>),
): Promise<T> {
  if (typeof value === "function") {
    return (value as () => MaybePromise<T>)();
  }
  return value;
}

function proofRequestHash(proof: QueryProof): string | null {
  return normalizeHash(
    proof.requestHash ??
      proof.queryHash ??
      proof.request_hash ??
      proof.query_hash,
  );
}

function proofResponseHash(proof: QueryProof): string | null {
  return normalizeHash(proof.responseHash ?? proof.response_hash);
}

function proofDeployment(proof: QueryProof): string | null {
  return normalizeDeployment(
    proof.deployment ??
      proof.deploymentId ??
      proof.subgraphDeployment ??
      proof.deployment_id ??
      proof.subgraph_deployment,
  );
}

function proofSignature(proof: QueryProof): string | null {
  const signature = proof.signature ?? proof.sig;
  if (typeof signature !== "string" || signature.trim().length === 0) {
    return null;
  }
  return signature.trim();
}

function proofAllocation(proof: QueryProof): string | null {
  const allocation =
    proof.allocation ?? proof.allocationId ?? proof.allocation_id;
  if (allocation === undefined || allocation === null || allocation === "") {
    return null;
  }
  return typeof allocation === "string" ? allocation : null;
}

export async function verifyQueryResponse(
  options: VerifyQueryResponseOptions,
): Promise<VerificationResult> {
  if (options === null || typeof options !== "object") {
    return failure("MISSING_DEPENDENCY", "Verification options are required");
  }

  const recoverSigner =
    options.recoverSigner ??
    options.recover ??
    options.recoverTypedDataAddress;

  if (typeof recoverSigner !== "function") {
    return failure(
      "MISSING_DEPENDENCY",
      "recoverSigner must be supplied by the gateway",
    );
  }

  if (
    options.allocations === undefined &&
    options.allocationTable === undefined &&
    options.activeAllocations === undefined &&
    options.getAllocation === undefined &&
    options.allocationLookup === undefined &&
    options.getActiveAllocation === undefined
  ) {
    return failure(
      "MISSING_DEPENDENCY",
      "An allocation table or getAllocation callback must be supplied",
    );
  }

  const chainHeadRaw = options.chainHead ?? options.chainHeadBlock;
  if (chainHeadRaw === undefined) {
    return failure(
      "MISSING_DEPENDENCY",
      "chainHead must be supplied by the gateway",
    );
  }

  let chainHead: bigint;
  try {
    const resolved = await resolveOption(chainHeadRaw);
    const parsed = parseInteger(resolved);
    if (parsed === null) {
      return failure(
        "INVALID_CHAIN_HEAD",
        "chainHead must be a non-negative integer",
        { chainHead: resolved },
      );
    }
    chainHead = parsed;
  } catch (error) {
    return failure(
      "INVALID_CHAIN_HEAD",
      "Unable to resolve chainHead",
      { error: String(error) },
    );
  }

  const requestBody = requestBodyFromOptions(options);
  const responseBody = responseBodyFromOptions(options);

  if (requestBody === undefined) {
    return failure(
      "MISSING_REQUEST_BODY",
      "The actual request body is required",
    );
  }
  if (responseBody === undefined) {
    return failure(
      "MISSING_RESPONSE_BODY",
      "The actual response body is required",
    );
  }

  const deploymentRaw = deploymentFromOptions(options, requestBody);
  const queryDeployment = normalizeDeployment(deploymentRaw);
  if (queryDeployment === null) {
    return failure(
      "MISSING_QUERY_DEPLOYMENT",
      "The query deployment is required",
    );
  }

  const proof = options.proof ?? options.attestation;
  if (proof === null || typeof proof !== "object") {
    return failure("MISSING_PROOF_FIELD", "A signed proof is required");
  }

  const expectedRequestHash = proofRequestHash(proof);
  if (expectedRequestHash === null) {
    return failure(
      "MISSING_PROOF_FIELD",
      "Proof requestHash must be a 32-byte hex string",
    );
  }

  const expectedResponseHash = proofResponseHash(proof);
  if (expectedResponseHash === null) {
    return failure(
      "MISSING_PROOF_FIELD",
      "Proof responseHash must be a 32-byte hex string",
    );
  }

  const expectedDeployment = proofDeployment(proof);
  if (expectedDeployment === null) {
    return failure(
      "MISSING_PROOF_FIELD",
      "Proof deployment must be a non-empty identifier",
    );
  }

  const signature = proofSignature(proof);
  if (signature === null) {
    return failure(
      "MISSING_PROOF_FIELD",
      "Proof signature is required",
    );
  }

  let actualRequestHash: string;
  let actualResponseHash: string;
  try {
    actualRequestHash = keccak256(requestBody);
    actualResponseHash = keccak256(responseBody);
  } catch (error) {
    return failure(
      "INVALID_HASH",
      "Unable to hash the request or response body",
      { error: String(error) },
    );
  }

  if (actualRequestHash !== expectedRequestHash) {
    return failure(
      "REQUEST_HASH_MISMATCH",
      "Proof requestHash does not match the actual request body",
      { actual: actualRequestHash, expected: expectedRequestHash },
    );
  }

  if (actualResponseHash !== expectedResponseHash) {
    return failure(
      "RESPONSE_HASH_MISMATCH",
      "Proof responseHash does not match the actual response body",
      { actual: actualResponseHash, expected: expectedResponseHash },
    );
  }

  if (!sameIdentifier(queryDeployment, expectedDeployment)) {
    return failure(
      "DEPLOYMENT_MISMATCH",
      "Proof deployment does not match the query deployment",
      {
        queryDeployment,
        proofDeployment: expectedDeployment,
      },
    );
  }

  const parsedResponse = parseJsonBody(responseBody);
  if (parsedResponse === null) {
    return failure(
      "INVALID_RESPONSE_BODY",
      "The response body must be a JSON object",
    );
  }

  if (responseHasErrors(parsedResponse)) {
    return failure(
      "RESPONSE_HAS_ERRORS",
      "GraphQL responses containing errors are not accepted",
    );
  }

  const metaBlock = metaBlockNumber(parsedResponse);
  if (metaBlock === null) {
    return failure(
      "MISSING_META_BLOCK",
      "Response does not contain a usable _meta.block.number",
    );
  }

  const numberGteRaw = numberGteFromOptions(options, requestBody);
  let numberGte: bigint | undefined;
  if (numberGteRaw !== undefined && numberGteRaw !== null) {
    const parsed = parseInteger(numberGteRaw);
    if (parsed === null) {
      return failure(
        "INVALID_NUMBER_GTE",
        "Query number_gte must be a non-negative integer",
        { numberGte: numberGteRaw },
      );
    }
    numberGte = parsed;
  }

  if (numberGte !== undefined && metaBlock < numberGte) {
    return failure(
      "BLOCK_BELOW_NUMBER_GTE",
      "Response block is below the query number_gte",
      {
        blockNumber: metaBlock.toString(),
        numberGte: numberGte.toString(),
      },
    );
  }

  if (metaBlock > chainHead) {
    return failure(
      "BLOCK_ABOVE_CHAIN_HEAD",
      "Response block is above the gateway chain head",
      {
        blockNumber: metaBlock.toString(),
        chainHead: chainHead.toString(),
      },
    );
  }

  const expectedIndexer = indexerFromOptions(options, requestBody);
  const typedData = buildDefaultTypedData(
    proof,
    actualRequestHash,
    actualResponseHash,
    queryDeployment,
    options,
  );

  const recoveryInput: QueryProofRecoveryInput = {
    ...typedData,
    signature,
    proof,
    typedData,
    requestHash: actualRequestHash,
    responseHash: actualResponseHash,
    deployment: queryDeployment,
  };

  let signerRaw: unknown;
  try {
    const recovered = recoverSigner(recoveryInput, typedData);
    signerRaw = typeof recovered === "object" && recovered !== null && "then" in recovered
      ? await recovered
      : recovered;
  } catch (error) {
    return failure(
      "SIGNATURE_RECOVERY_FAILED",
      "EIP-712 signature recovery failed",
      { error: String(error) },
    );
  }

  const signer = normalizeAddress(signerRaw);
  if (signer === null || signer === "0x0000000000000000000000000000000000000000") {
    return failure(
      "INVALID_SIGNER",
      "Recovered signer must be a valid non-zero address",
      { signer: signerRaw },
    );
  }

  const proofAllocationValue = proofAllocation(proof);
  if (
    proofAllocationValue !== null &&
    normalizeAddress(proofAllocationValue) !== signer
  ) {
    return failure(
      "PROOF_ALLOCATION_MISMATCH",
      "Recovered signer is not the allocation named in the proof",
    );
  }

  const lookup =
    options.getAllocation ??
    options.allocationLookup ??
    options.getActiveAllocation;
  const allocationSource =
    options.allocations ??
    options.allocationTable ??
    options.activeAllocations;
  const lookupFromSource =
    typeof allocationSource === "function"
      ? (allocationSource as AllocationLookup)
      : undefined;
  const allocationTable =
    typeof allocationSource === "function"
      ? undefined
      : allocationSource;
  let allocation: AllocationRecord | null;
  try {
    allocation = await lookupAllocation(
      allocationTable,
      lookup ?? lookupFromSource,
      signer,
      queryDeployment,
      expectedIndexer,
    );
  } catch (error) {
    return failure(
      "ALLOCATION_NOT_FOUND",
      "Allocation lookup failed",
      { error: String(error) },
    );
  }

  if (allocation === null) {
    return failure(
      "ALLOCATION_NOT_FOUND",
      "Recovered signer is not an allocation for this deployment",
      { signer, deployment: queryDeployment },
    );
  }

  const allocationId = allocationIdentifier(allocation);
  if (allocationId === null || normalizeAddress(allocationId) !== signer) {
    return failure(
      "ALLOCATION_NOT_FOUND",
      "Recovered signer is not the allocation identifier",
      { signer, allocationId },
    );
  }

  const allocationDeploymentValue = allocationDeployment(allocation);
  if (
    allocationDeploymentValue !== null &&
    !sameIdentifier(queryDeployment, normalizeDeployment(allocationDeploymentValue) ?? "")
  ) {
    return failure(
      "ALLOCATION_DEPLOYMENT_MISMATCH",
      "The active allocation belongs to a different deployment",
      {
        queryDeployment,
        allocationDeployment: allocationDeploymentValue,
      },
    );
  }

  if (!recordActiveState(allocation)) {
    return failure(
      "ALLOCATION_NOT_ACTIVE",
      "The allocation is closed or inactive",
      { allocationId: signer },
    );
  }

  if (expectedIndexer !== undefined) {
    const allocationIndexerValue = allocationIndexer(allocation);
    if (
      allocationIndexerValue === null ||
      !sameIdentifier(expectedIndexer, allocationIndexerValue)
    ) {
      return failure(
        "ALLOCATION_INDEXER_MISMATCH",
        "The allocation does not belong to the expected indexer",
        {
          expectedIndexer,
          allocationIndexer: allocationIndexerValue,
        },
      );
    }
  }

  const blockNumber = toSafeNumber(metaBlock);
  if (blockNumber === null) {
    return failure(
      "INVALID_META_BLOCK",
      "Response block number exceeds the safe integer range",
    );
  }

  const chainHeadNumber = toSafeNumber(chainHead);
  if (chainHeadNumber === null) {
    return failure(
      "INVALID_CHAIN_HEAD",
      "chainHead exceeds the safe integer range",
    );
  }

  const numberGteNumber =
    numberGte === undefined ? undefined : toSafeNumber(numberGte);

  const success: VerificationSuccess = {
    ok: true,
    valid: true,
    isValid: true,
    accepted: true,
    error: null,
    signer,
    allocation,
    deployment: queryDeployment,
    requestHash: actualRequestHash,
    responseHash: actualResponseHash,
    blockNumber,
    blockNumberBigInt: metaBlock,
    numberGte: numberGteNumber ?? undefined,
    numberGteBigInt: numberGte,
    chainHead: chainHeadNumber,
    chainHeadBigInt: chainHead,
  };

  return success;
}

export const verifyQueryProof = verifyQueryResponse;
export const verifyResponseProof = verifyQueryResponse;
export const verifyIndexerResponse = verifyQueryResponse;
export const verifyQueryAttestation = verifyQueryResponse;
export const verifyProof = verifyQueryResponse;
export const validateQueryResponse = verifyQueryResponse;

export async function isValidQueryResponse(
  options: VerifyQueryResponseOptions,
): Promise<boolean> {
  return (await verifyQueryResponse(options)).ok;
}

export class QueryResponseVerificationError extends Error {
  readonly result: VerificationFailure;

  constructor(result: VerificationFailure) {
    super(`${result.code}: ${result.reason}`);
    this.name = "QueryResponseVerificationError";
    this.result = result;
  }
}

export async function assertValidQueryResponse(
  options: VerifyQueryResponseOptions,
): Promise<VerificationSuccess> {
  const result = await verifyQueryResponse(options);
  if (!result.ok) {
    throw new QueryResponseVerificationError(result);
  }
  return result;
}

export interface VerifierDependencies {
  chainHead: VerifyQueryResponseOptions["chainHead"];
  recoverSigner: RecoverSigner;
  allocations?: AllocationTable;
  getAllocation?: AllocationLookup;
  indexer?: string;
  domain?: Record<string, unknown>;
  types?: Record<string, readonly Eip712Field[]>;
  primaryType?: string;
}

export function createQueryResponseVerifier(
  dependencies: VerifierDependencies,
) {
  return (options: Omit<VerifyQueryResponseOptions,
    | "chainHead"
    | "recoverSigner"
    | "allocations"
    | "getAllocation"
    | "indexer"
    | "domain"
    | "types"
    | "primaryType"
  > & Partial<VerifierDependencies>): Promise<VerificationResult> =>
    verifyQueryResponse({ ...dependencies, ...options });
}

export const createQueryProofVerifier = createQueryResponseVerifier;
