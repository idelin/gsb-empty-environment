import {
  bodyToBytes,
  hashBody,
  isResponseObject,
  parseBody,
  type BodyInput,
} from "./body.ts";
import {
  buildResponseProofTypedData,
  DEFAULT_RESPONSE_PROOF_DOMAIN,
  hashTypedDataHex,
  type Eip712Domain,
  type Eip712TypedData,
} from "./eip712.ts";
import { isBytes32, isHex } from "./hash.ts";

export type MaybePromise<T> = T | Promise<T>;
export type Numeric = number | bigint | string;

export interface ResponseProof {
  readonly requestHash: string;
  readonly responseHash: string;
  readonly deployment: string;
  readonly signature: string;
}

export interface QueryContext {
  readonly indexer: string;
  readonly deployment: string;
  readonly numberGte?: Numeric;
  /** Alias for numberGte when the gateway keeps GraphQL variable names. */
  readonly number_gte?: Numeric;
}

export interface VerifyResponseInput {
  readonly requestBody: BodyInput;
  readonly responseBody: BodyInput;
  readonly proof: ResponseProof;
  readonly query: QueryContext;
  readonly chainHead: Numeric;
}

export interface ResponseProofRecoveryInput {
  readonly typedData: Eip712TypedData;
  readonly digest: string;
  readonly signature: string;
}

export type RecoverSigner = (
  input: ResponseProofRecoveryInput,
) => MaybePromise<string>;

export interface AllocationQuery {
  readonly indexer: string;
  readonly deployment: string;
  readonly chainHead: bigint;
}

export interface AllocationRecord {
  readonly allocationId?: string;
  readonly id?: string;
  readonly indexer?: string;
  readonly deployment?: string;
  readonly active?: boolean;
  readonly closed?: boolean;
  readonly closedAtBlock?: Numeric | null;
}

export type AllocationLike = string | AllocationRecord;

export type AllocationResult =
  | readonly AllocationLike[]
  | AllocationLike
  | null
  | undefined;

export interface AllocationTableObject {
  getActiveAllocations?(
    query: AllocationQuery,
  ): MaybePromise<AllocationResult>;
  /** Optional full table; the verifier filters closed/expired records itself. */
  getAllocations?(query: AllocationQuery): MaybePromise<AllocationResult>;
  getActiveAllocation?(query: AllocationQuery): MaybePromise<AllocationResult>;
}

export type AllocationTable =
  | AllocationTableObject
  | ((query: AllocationQuery) => MaybePromise<AllocationResult>);

export interface VerifierDependencies {
  readonly recoverSigner: RecoverSigner;
  readonly allocationTable: AllocationTable;
  readonly eip712Domain?: Eip712Domain;
}

export type FailureCode =
  | "INVALID_INPUT"
  | "PROOF_MALFORMED"
  | "RESPONSE_MALFORMED"
  | "RESPONSE_HAS_ERRORS"
  | "REQUEST_HASH_MISMATCH"
  | "RESPONSE_HASH_MISMATCH"
  | "DEPLOYMENT_MISMATCH"
  | "SIGNATURE_RECOVERY_FAILED"
  | "SIGNER_NOT_ACTIVE_ALLOCATION"
  | "ALLOCATION_LOOKUP_FAILED"
  | "BLOCK_NUMBER_MISSING"
  | "BLOCK_NUMBER_INVALID"
  | "BLOCK_BELOW_MINIMUM"
  | "BLOCK_ABOVE_CHAIN_HEAD"
  | "UNEXPECTED_ERROR";

export interface VerificationSuccess {
  readonly ok: true;
  readonly code: "OK";
  readonly signer: string;
  readonly allocationId: string;
  readonly requestHash: string;
  readonly responseHash: string;
  readonly blockNumber: bigint;
}

export interface VerificationFailure {
  readonly ok: false;
  readonly code: FailureCode;
  readonly message: string;
}

export type VerificationResult = VerificationSuccess | VerificationFailure;

function failure(code: FailureCode, message: string): VerificationFailure {
  return { ok: false, code, message };
}

function parseInteger(value: unknown, label: string): bigint {
  if (typeof value === "bigint") {
    if (value < 0n) {
      throw new TypeError(`${label} must be non-negative`);
    }
    return value;
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new TypeError(`${label} must be a non-negative safe integer`);
    }
    return BigInt(value);
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (/^0x[0-9a-fA-F]+$/.test(trimmed)) {
      return BigInt(trimmed);
    }
    if (/^[0-9]+$/.test(trimmed)) {
      return BigInt(trimmed);
    }
  }
  throw new TypeError(`${label} must be a non-negative integer`);
}

function normalizeHex(value: string): string {
  return value.toLowerCase();
}

function normalizeDeployment(value: string): string {
  return normalizeHex(value);
}

function normalizeIdentifier(value: string): string {
  return value.toLowerCase();
}

function parseQuery(
  query: QueryContext,
): { indexer: string; deployment: string; numberGte: bigint } {
  if (query === null || typeof query !== "object") {
    throw new TypeError("query must be an object");
  }
  if (typeof query.indexer !== "string" || query.indexer.length === 0) {
    throw new TypeError("query.indexer must be a non-empty string");
  }
  if (!isBytes32(query.deployment)) {
    throw new TypeError("query.deployment must be a 32-byte hex value");
  }

  const numberGteValue =
    query.numberGte !== undefined ? query.numberGte : query.number_gte;
  const numberGte =
    numberGteValue === undefined ? 0n : parseInteger(numberGteValue, "number_gte");

  return {
    indexer: query.indexer,
    deployment: normalizeDeployment(query.deployment),
    numberGte,
  };
}

function parseProof(proof: ResponseProof): ResponseProof {
  if (proof === null || typeof proof !== "object") {
    throw new TypeError("proof must be an object");
  }
  if (!isBytes32(proof.requestHash)) {
    throw new TypeError("proof.requestHash must be a 32-byte hex value");
  }
  if (!isBytes32(proof.responseHash)) {
    throw new TypeError("proof.responseHash must be a 32-byte hex value");
  }
  if (!isBytes32(proof.deployment)) {
    throw new TypeError("proof.deployment must be a 32-byte hex value");
  }
  if (
    typeof proof.signature !== "string" ||
    !isHex(proof.signature) ||
    proof.signature.length <= 2 ||
    proof.signature.length % 2 !== 0
  ) {
    throw new TypeError("proof.signature must be a 0x-prefixed hex string");
  }
  return proof;
}

function extractBlockNumber(response: Record<string, unknown>): bigint {
  const data = isResponseObject(response.data) ? response.data : undefined;
  const dataMeta = data && isResponseObject(data._meta) ? data._meta : undefined;
  const topMeta = isResponseObject(response._meta)
    ? response._meta
    : undefined;

  const values: unknown[] = [];
  for (const meta of [dataMeta, topMeta]) {
    if (!meta || !isResponseObject(meta.block)) {
      continue;
    }
    if (meta.block.number !== undefined && meta.block.number !== null) {
      values.push(meta.block.number);
    }
  }

  if (values.length === 0) {
    throw new Error("missing _meta.block.number");
  }

  const parsed = values.map((value) => parseInteger(value, "_meta.block.number"));
  if (parsed.some((value) => value !== parsed[0])) {
    throw new RangeError("conflicting _meta.block.number values");
  }
  return parsed[0];
}

function allocationIdOf(record: AllocationRecord): string | undefined {
  const id = record.allocationId ?? record.id;
  if (typeof id !== "string" || id.length === 0) {
    return undefined;
  }
  return id;
}

function isActiveAt(
  record: AllocationRecord,
  query: AllocationQuery,
): boolean {
  if (record.active === false || record.closed === true) {
    return false;
  }
  if (
    record.indexer !== undefined &&
    normalizeIdentifier(record.indexer) !== normalizeIdentifier(query.indexer)
  ) {
    return false;
  }
  if (
    record.deployment !== undefined &&
    normalizeDeployment(record.deployment) !== query.deployment
  ) {
    return false;
  }
  if (record.closedAtBlock !== undefined && record.closedAtBlock !== null) {
    let closedAtBlock: bigint;
    try {
      closedAtBlock = parseInteger(record.closedAtBlock, "closedAtBlock");
    } catch {
      return false;
    }
    if (closedAtBlock <= query.chainHead) {
      return false;
    }
  }
  return allocationIdOf(record) !== undefined;
}

async function lookupAllocations(
  table: AllocationTable,
  query: AllocationQuery,
): Promise<readonly AllocationLike[]> {
  let result: AllocationResult;

  if (typeof table === "function") {
    result = await table(query);
  } else if (typeof table.getActiveAllocations === "function") {
    result = await table.getActiveAllocations(query);
  } else if (typeof table.getAllocations === "function") {
    result = await table.getAllocations(query);
  } else if (typeof table.getActiveAllocation === "function") {
    result = await table.getActiveAllocation(query);
  } else {
    throw new TypeError(
      "allocationTable must be a function or expose getActiveAllocations/getAllocations/getActiveAllocation",
    );
  }

  if (result === null || result === undefined) {
    return [];
  }
  if (Array.isArray(result)) {
    return result;
  }
  return [result as AllocationLike];
}

function activeAllocationIds(
  allocations: readonly AllocationLike[],
  query: AllocationQuery,
): string[] {
  const ids: string[] = [];
  for (const entry of allocations) {
    if (typeof entry === "string") {
      if (entry.length > 0) {
        ids.push(entry);
      }
      continue;
    }
    if (entry !== null && typeof entry === "object" && isActiveAt(entry, query)) {
      const id = allocationIdOf(entry);
      if (id !== undefined) {
        ids.push(id);
      }
    }
  }
  return ids;
}

export function responseProofDigest(
  proof: ResponseProofDigestInput,
  domain: Eip712Domain = DEFAULT_RESPONSE_PROOF_DOMAIN,
): string {
  return hashTypedDataHex(
    buildResponseProofTypedData(
      {
        requestHash: proof.requestHash,
        responseHash: proof.responseHash,
        deployment: proof.deployment,
      },
      domain,
    ),
  );
}

export interface ResponseProofDigestInput {
  readonly requestHash: string;
  readonly responseHash: string;
  readonly deployment: string;
}

export async function verifyResponseProof(
  input: VerifyResponseInput,
  dependencies: VerifierDependencies,
): Promise<VerificationResult> {
  try {
    if (input === null || typeof input !== "object") {
      return failure("INVALID_INPUT", "input must be an object");
    }
    if (dependencies === null || typeof dependencies !== "object") {
      return failure("INVALID_INPUT", "dependencies must be an object");
    }
    if (typeof dependencies.recoverSigner !== "function") {
      return failure("INVALID_INPUT", "recoverSigner must be a function");
    }
    if (
      dependencies.allocationTable === null ||
      dependencies.allocationTable === undefined
    ) {
      return failure("INVALID_INPUT", "allocationTable is required");
    }

    let query: ReturnType<typeof parseQuery>;
    try {
      query = parseQuery(input.query);
    } catch (error) {
      return failure(
        "INVALID_INPUT",
        error instanceof Error ? error.message : "invalid query",
      );
    }

    let proof: ResponseProof;
    try {
      proof = parseProof(input.proof);
    } catch (error) {
      return failure(
        "PROOF_MALFORMED",
        error instanceof Error ? error.message : "invalid proof",
      );
    }
    let chainHead: bigint;
    try {
      chainHead = parseInteger(input.chainHead, "chainHead");
    } catch (error) {
      return failure(
        "INVALID_INPUT",
        error instanceof Error ? error.message : "invalid chainHead",
      );
    }

    let requestHash: string;
    try {
      requestHash = hashBody(input.requestBody);
    } catch (error) {
      return failure(
        "INVALID_INPUT",
        error instanceof Error
          ? `requestBody cannot be hashed: ${error.message}`
          : "requestBody cannot be hashed",
      );
    }

    let responseHash: string;
    try {
      responseHash = hashBody(input.responseBody);
    } catch (error) {
      return failure(
        "RESPONSE_MALFORMED",
        error instanceof Error
          ? `responseBody cannot be hashed: ${error.message}`
          : "responseBody cannot be hashed",
      );
    }

    if (normalizeHex(proof.requestHash) !== requestHash) {
      return failure(
        "REQUEST_HASH_MISMATCH",
        "proof.requestHash does not match keccak256(request body)",
      );
    }
    if (normalizeHex(proof.responseHash) !== responseHash) {
      return failure(
        "RESPONSE_HASH_MISMATCH",
        "proof.responseHash does not match keccak256(response body)",
      );
    }

    let response: Record<string, unknown>;
    try {
      const parsed = parseBody(input.responseBody);
      if (!isResponseObject(parsed)) {
        return failure(
          "RESPONSE_MALFORMED",
          "response body must be a JSON object",
        );
      }
      response = parsed;
    } catch (error) {
      return failure(
        "RESPONSE_MALFORMED",
        error instanceof Error
          ? `response body is not valid JSON: ${error.message}`
          : "response body is not valid JSON",
      );
    }

    if (
      Object.prototype.hasOwnProperty.call(response, "errors") &&
      response.errors !== null &&
      response.errors !== undefined
    ) {
      return failure(
        "RESPONSE_HAS_ERRORS",
        "responses containing an errors member are not accepted",
      );
    }

    let blockNumber: bigint;
    try {
      blockNumber = extractBlockNumber(response);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "invalid _meta.block.number";
      return failure(
        message.includes("missing") ? "BLOCK_NUMBER_MISSING" : "BLOCK_NUMBER_INVALID",
        message,
      );
    }

    if (blockNumber < query.numberGte) {
      return failure(
        "BLOCK_BELOW_MINIMUM",
        `response block ${blockNumber} is below number_gte ${query.numberGte}`,
      );
    }
    if (blockNumber > chainHead) {
      return failure(
        "BLOCK_ABOVE_CHAIN_HEAD",
        `response block ${blockNumber} is above chain head ${chainHead}`,
      );
    }

    if (normalizeDeployment(proof.deployment) !== query.deployment) {
      return failure(
        "DEPLOYMENT_MISMATCH",
        "proof deployment does not match the queried deployment",
      );
    }

    const domain = dependencies.eip712Domain ?? DEFAULT_RESPONSE_PROOF_DOMAIN;
    const typedData = buildResponseProofTypedData(
      {
        requestHash: proof.requestHash,
        responseHash: proof.responseHash,
        deployment: proof.deployment,
      },
      domain,
    );
    const digest = hashTypedDataHex(typedData);

    let signer: string;
    try {
      signer = await dependencies.recoverSigner({
        typedData,
        digest,
        signature: proof.signature,
      });
    } catch (error) {
      return failure(
        "SIGNATURE_RECOVERY_FAILED",
        error instanceof Error ? error.message : "signature recovery failed",
      );
    }
    if (typeof signer !== "string" || signer.length === 0) {
      return failure(
        "SIGNATURE_RECOVERY_FAILED",
        "recoverSigner did not return a signer identifier",
      );
    }

    const allocationQuery: AllocationQuery = {
      indexer: query.indexer,
      deployment: query.deployment,
      chainHead,
    };
    let allocations: readonly AllocationLike[];
    try {
      allocations = await lookupAllocations(
        dependencies.allocationTable,
        allocationQuery,
      );
    } catch (error) {
      return failure(
        "ALLOCATION_LOOKUP_FAILED",
        error instanceof Error
          ? error.message
          : "allocation lookup failed",
      );
    }

    const activeIds = activeAllocationIds(allocations, allocationQuery);
    const normalizedSigner = normalizeIdentifier(signer);
    const matchingId = activeIds.find(
      (id) => normalizeIdentifier(id) === normalizedSigner,
    );
    if (matchingId === undefined) {
      return failure(
        "SIGNER_NOT_ACTIVE_ALLOCATION",
        "recovered signer is not an active allocation for this indexer and deployment",
      );
    }

    return {
      ok: true,
      code: "OK",
      signer,
      allocationId: matchingId,
      requestHash,
      responseHash,
      blockNumber,
    };
  } catch (error) {
    return failure(
      "UNEXPECTED_ERROR",
      error instanceof Error ? error.message : "unexpected verification error",
    );
  }
}

export function createResponseVerifier(dependencies: VerifierDependencies) {
  return (input: VerifyResponseInput): Promise<VerificationResult> =>
    verifyResponseProof(input, dependencies);
}

export async function isValidResponseProof(
  input: VerifyResponseInput,
  dependencies: VerifierDependencies,
): Promise<boolean> {
  return (await verifyResponseProof(input, dependencies)).ok;
}

export class ResponseProofError extends Error {
  readonly code: FailureCode;

  constructor(code: FailureCode, message: string) {
    super(message);
    this.name = "ResponseProofError";
    this.code = code;
  }
}

export async function assertResponseProof(
  input: VerifyResponseInput,
  dependencies: VerifierDependencies,
): Promise<VerificationSuccess> {
  const result = await verifyResponseProof(input, dependencies);
  if (!result.ok) {
    throw new ResponseProofError(result.code, result.message);
  }
  return result;
}

export class InMemoryAllocationTable implements AllocationTableObject {
  private records: AllocationRecord[];

  constructor(records: readonly AllocationRecord[] = []) {
    this.records = records.map((record) => ({ ...record }));
  }

  add(record: AllocationRecord): this {
    this.records.push({ ...record });
    return this;
  }

  close(allocationId: string, atBlock: Numeric): this {
    const normalized = normalizeIdentifier(allocationId);
    this.records = this.records.map((record) => {
      const id = allocationIdOf(record);
      if (id !== undefined && normalizeIdentifier(id) === normalized) {
        return { ...record, active: false, closed: true, closedAtBlock: atBlock };
      }
      return record;
    });
    return this;
  }

  getActiveAllocations(query: AllocationQuery): readonly AllocationLike[] {
    return this.records
      .filter(
        (record) =>
          normalizeIdentifier(record.indexer ?? query.indexer) ===
            normalizeIdentifier(query.indexer) &&
          normalizeDeployment(record.deployment ?? query.deployment) ===
            query.deployment &&
          isActiveAt(record, query),
      )
      .map((record) => ({ ...record }));
  }
}

export { bodyToBytes, hashBody };
