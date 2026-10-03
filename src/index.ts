export type { TrajectoryPoint, ContactInterval, Vec3 } from "./types.ts";
export { SpacetimeGridIndex, type GridIndexOptions } from "./grid_index.ts";
export {
  computeContactIntervals,
  positionAt,
  type RawContactInterval,
} from "./intersection.ts";
export {
  commitTrajectory,
  selectWitnesses,
  verifyCertificate,
  hashPoint,
  type ContactCertificate,
  type TrajectoryCommitment,
} from "./certificate.ts";
export { MerkleTree, type MerkleProofStep } from "./merkle.ts";
export {
  IntersectionProver,
  verifyProof,
  type IntersectionProof,
  type ProofVerification,
} from "./prover.ts";
