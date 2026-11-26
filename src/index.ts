/**
 * 联邦社交收件箱所有权判定组件 —— 可导入公共入口。
 *
 * @example
 *   import { OwnershipEngine, replayAll, assertConvergent } from './src/index.ts';
 */

export { OwnershipEngine, evidence, hashObjectState, withoutFields } from './engine.ts';
export {
  replayAll,
  replayPermutations,
  replayFromCheckpoint,
  builtinPermutations,
  assertConvergent,
  statesEqual,
  collectEvidence,
} from './replay.ts';
export type { ReplayReport } from './replay.ts';
export {
  canonicalize,
  canonicalBytes,
  hashCanonical,
  hashText,
  structuredCloneCanonical,
} from './canonical.ts';
export {
  generateEd25519KeyPair,
  generateSigningKey,
  signMessage,
  verifySignature,
  isUsableEd25519PublicKey,
} from './signing.ts';
export type { KeyPairMaterial, SigningKey } from './signing.ts';
export type {
  ActorId,
  AliasBindPayload,
  AliasInfo,
  AliasRetirePayload,
  AnyPayload,
  AppliedEdit,
  ChainNode,
  ChainNodeId,
  CheckpointCompressPayload,
  CheckpointSnapshot,
  DelegationGrantPayload,
  DelegationRevokePayload,
  DomainEvent,
  EditDecision,
  EngineOptions,
  EventId,
  Evidence,
  FieldEditPayload,
  ForkRecoveryPayload,
  GrantId,
  GrantRecord,
  IngestResult,
  KeyId,
  KeyPublishPayload,
  KeyRevokePayload,
  Nonce,
  ObjectId,
  ObjectRegistrationPayload,
  ObjectState,
  OwnershipState,
  OwnershipTransferPayload,
  PublicKeyInfo,
  QuorumRule,
  TransportEnvelope,
  Verdict,
} from './types.ts';
