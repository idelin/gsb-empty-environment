import type { HashableBody } from "./keccak.ts";

export type MaybePromise<T> = T | Promise<T>;
export type BlockNumberish = bigint | number | string;

export interface QueryProof {
  requestHash?: string;
  queryHash?: string;
  request_hash?: string;
  query_hash?: string;
  responseHash?: string;
  response_hash?: string;
  deployment?: string;
  deploymentId?: string;
  subgraphDeployment?: string;
  deployment_id?: string;
  subgraph_deployment?: string;
  signature?: string;
  sig?: string;
  allocation?: string;
  allocationId?: string;
  allocation_id?: string;
  [key: string]: unknown;
}

export interface QueryRequestContext {
  body: HashableBody;
  deployment?: string;
  deploymentId?: string;
  numberGte?: BlockNumberish;
  number_gte?: BlockNumberish;
  indexer?: string;
  indexerId?: string;
}

export interface QueryResponseContext {
  body: HashableBody;
}

export interface AllocationRecord {
  allocationId?: string;
  allocation?: string;
  id?: string;
  address?: string;
  indexer?: string;
  indexerId?: string;
  deployment?: string;
  deploymentId?: string;
  subgraphDeployment?: string;
  subgraphDeploymentID?: string;
  active?: boolean | number | string;
  isActive?: boolean | number | string;
  status?: string;
  closedAt?: unknown;
  [key: string]: unknown;
}

export type AllocationValue =
  | AllocationRecord
  | boolean
  | string
  | readonly string[]
  | null
  | undefined;

export type AllocationTable =
  | ReadonlyMap<string, AllocationValue>
  | ReadonlySet<string>
  | readonly (AllocationRecord | string)[]
  | Readonly<Record<string, AllocationValue>>;

export type AllocationLookup = (
  allocationId: string,
  deployment?: string,
  indexer?: string,
) => MaybePromise<AllocationValue>;

export interface Eip712Field {
  name: string;
  type: string;
}

export interface QueryProofTypedData {
  domain: Record<string, unknown>;
  types: Record<string, readonly Eip712Field[]>;
  primaryType: string;
  message: Record<string, unknown>;
}

export interface QueryProofRecoveryInput extends QueryProofTypedData {
  signature: string;
  proof: QueryProof;
  typedData: QueryProofTypedData;
  requestHash: string;
  responseHash: string;
  deployment: string;
}

/**
 * Injected EIP-712 recovery. A viem-style `recoverTypedDataAddress` may be
 * passed directly when the default typed-data schema is used. The second
 * argument is always the typed data, for adapters that separate it.
 */
export type RecoverSigner = (
  input: QueryProofRecoveryInput,
  typedData: QueryProofTypedData,
) => MaybePromise<string>;

export interface VerifyQueryResponseOptions {
  /** Canonical nested form. */
  request?: QueryRequestContext | HashableBody;
  response?: QueryResponseContext | HashableBody;
  proof?: QueryProof;
  attestation?: QueryProof;

  chainHead: BlockNumberish | (() => MaybePromise<BlockNumberish>);
  recoverSigner?: RecoverSigner;
  recover?: RecoverSigner;
  recoverTypedDataAddress?: RecoverSigner;
  allocations?: AllocationTable | AllocationLookup;
  allocationTable?: AllocationTable | AllocationLookup;
  activeAllocations?: AllocationTable | AllocationLookup;
  getAllocation?: AllocationLookup;
  allocationLookup?: AllocationLookup;

  /** Optional expected indexer. When set, its allocation must be used. */
  indexer?: string;
  expectedIndexer?: string;
  indexerId?: string;

  /** EIP-712 domain/schema overrides. */
  domain?: Record<string, unknown>;
  types?: Record<string, readonly Eip712Field[]>;
  primaryType?: string;
  typedData?:
    | QueryProofTypedData
    | ((proof: QueryProof) => QueryProofTypedData);

  /** Flat aliases accepted for gateway integration convenience. */
  requestBody?: HashableBody;
  queryBody?: HashableBody;
  query?: QueryRequestContext | HashableBody;
  responseBody?: HashableBody;
  deployment?: string;
  deploymentId?: string;
  queryDeployment?: string;
  numberGte?: BlockNumberish;
  number_gte?: BlockNumberish;
  chainHeadBlock?: BlockNumberish | (() => MaybePromise<BlockNumberish>);
  getActiveAllocation?: AllocationLookup;
}

export type VerificationFailureCode =
  | "MISSING_DEPENDENCY"
  | "INVALID_CHAIN_HEAD"
  | "MISSING_REQUEST_BODY"
  | "MISSING_RESPONSE_BODY"
  | "MISSING_QUERY_DEPLOYMENT"
  | "MISSING_PROOF_FIELD"
  | "INVALID_HASH"
  | "INVALID_DEPLOYMENT"
  | "REQUEST_HASH_MISMATCH"
  | "RESPONSE_HASH_MISMATCH"
  | "DEPLOYMENT_MISMATCH"
  | "SIGNATURE_RECOVERY_FAILED"
  | "INVALID_SIGNER"
  | "PROOF_ALLOCATION_MISMATCH"
  | "ALLOCATION_NOT_FOUND"
  | "ALLOCATION_NOT_ACTIVE"
  | "ALLOCATION_DEPLOYMENT_MISMATCH"
  | "ALLOCATION_INDEXER_MISMATCH"
  | "INVALID_RESPONSE_BODY"
  | "RESPONSE_HAS_ERRORS"
  | "MISSING_META_BLOCK"
  | "INVALID_META_BLOCK"
  | "INVALID_NUMBER_GTE"
  | "BLOCK_BELOW_NUMBER_GTE"
  | "BLOCK_ABOVE_CHAIN_HEAD";

export interface VerificationSuccess {
  ok: true;
  valid: true;
  isValid: true;
  accepted: true;
  error: null;
  signer: string;
  allocation: AllocationRecord;
  deployment: string;
  requestHash: string;
  responseHash: string;
  blockNumber: number;
  blockNumberBigInt: bigint;
  numberGte?: number;
  numberGteBigInt?: bigint;
  chainHead: number;
  chainHeadBigInt: bigint;
}

export interface VerificationFailure {
  ok: false;
  valid: false;
  isValid: false;
  accepted: false;
  error: { code: VerificationFailureCode; message: string };
  code: VerificationFailureCode;
  reason: string;
  details?: Record<string, unknown>;
}

export type VerificationResult = VerificationSuccess | VerificationFailure;
