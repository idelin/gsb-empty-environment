export type GovernanceErrorCode =
  | "INVALID_POSITION"
  | "DELEGATION_CYCLE"
  | "UNKNOWN_PROPOSAL"
  | "DUPLICATE_PROPOSAL"
  | "VOTING_NOT_OPEN"
  | "VOTE_ALREADY_CAST"
  | "NO_VOTING_POWER"
  | "VOTING_CLOSED"
  | "PROPOSAL_NOT_SUCCEEDED"
  | "QUEUE_NOT_OPEN"
  | "ALREADY_QUEUED"
  | "ALREADY_EXECUTED"
  | "NOT_QUEUED"
  | "EXECUTION_NOT_OPEN"
  | "ACTION_DIGEST_MISMATCH"
  | "CONFIG_GENERATION_MISMATCH"
  | "INVALID_CONFIG"
  | "INVALID_MODEL";

export class GovernanceError extends Error {
  readonly code: GovernanceErrorCode;

  constructor(code: GovernanceErrorCode, message: string) {
    super(message);
    this.name = "GovernanceError";
    this.code = code;
  }
}

export function governanceError(
  code: GovernanceErrorCode,
  message: string
): never {
  throw new GovernanceError(code, message);
}

