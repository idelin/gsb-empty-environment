export type StableId = string;
export type IdInput = StableId | number;

export type MessageVisibility = "public" | "private";

export interface InstanceSpec {
  id: IdInput;
  principal?: IdInput;
  connection?: IdInput;
  connectionId?: IdInput;
  transport?: IdInput;
  generation?: number;
  interruptible?: boolean;
  canReauthenticate?: boolean;
  stableOrder?: number;
}

export type InstanceCollection =
  | readonly (InstanceSpec | IdInput)[]
  | Readonly<Record<string, Omit<InstanceSpec, "id">>>;

export interface ConnectionSpec {
  id: IdInput;
  instances?: readonly IdInput[];
}

export interface FrameSpec {
  id: IdInput;
  candidates?: readonly IdInput[];
  receiptCandidates?: readonly IdInput[];
  possibleReceivers?: readonly IdInput[];
  receiverCandidates?: readonly IdInput[];
  channels?: readonly IdInput[];
  queues?: readonly IdInput[];
  optional?: boolean;
  required?: boolean;
  generation?: number;
}

export interface ChannelSpec {
  id: IdInput;
  frame?: IdInput;
  frameId?: IdInput;
  candidates?: readonly IdInput[];
  receiptCandidates?: readonly IdInput[];
  possibleReceivers?: readonly IdInput[];
  receiverCandidates?: readonly IdInput[];
  mutexGroup?: IdInput;
  optional?: boolean;
  required?: boolean;
  stop?: boolean;
}

export interface MessageSpec {
  id?: IdInput;
  channel?: IdInput;
  channelId?: IdInput;
  queue?: IdInput;
  queueId?: IdInput;
  channels?: readonly IdInput[];
  frame?: IdInput;
  frameId?: IdInput;
  kind?: string;
  visibility?: MessageVisibility | string;
  private?: boolean;
  public?: boolean;
  audience?: IdInput | readonly IdInput[];
  recipients?: IdInput | readonly IdInput[];
  to?: IdInput | readonly IdInput[];
  recipient?: IdInput | readonly IdInput[];
  privateTo?: IdInput | readonly IdInput[];
  authorizedPrincipals?: IdInput | readonly IdInput[];
  allowedPrincipals?: IdInput | readonly IdInput[];
  authorizedInstances?: IdInput | readonly IdInput[];
  sender?: IdInput;
  scope?: IdInput;
  seq?: number;
  at?: number;
  candidates?: readonly IdInput[];
  ack?: readonly IdInput[] | { candidates?: readonly IdInput[]; receivers?: readonly IdInput[] };
  receivers?: readonly IdInput[];
  receiverCandidates?: readonly IdInput[];
  possibleReceivers?: readonly IdInput[];
  confirmed?: boolean;
  acknowledged?: boolean;
  protected?: boolean;
  optional?: boolean;
  required?: boolean;
}

export interface GrantSpec {
  id?: IdInput;
  from?: IdInput;
  grantor?: IdInput;
  by?: IdInput;
  sender?: IdInput;
  fromKind?: "principal" | "instance";
  grantorType?: "principal" | "instance";
  to?: IdInput;
  toInstance?: IdInput;
  grantee?: IdInput;
  instance?: IdInput;
  receiver?: IdInput;
  audience?: IdInput | readonly IdInput[];
  principal?: IdInput;
  capability?: IdInput;
  scope?: IdInput;
  generation?: number;
  gen?: number;
  version?: number;
  seq?: number;
  at?: number;
  active?: boolean;
}

export interface RevokeSpec {
  id?: IdInput;
  grant?: IdInput;
  grantId?: IdInput;
  permit?: IdInput;
  permitId?: IdInput;
  delegation?: IdInput;
  delegationId?: IdInput;
  target?: IdInput;
  grantee?: IdInput;
  to?: IdInput;
  toInstance?: IdInput;
  instance?: IdInput;
  receiver?: IdInput;
  grantor?: IdInput;
  from?: IdInput;
  audience?: IdInput | readonly IdInput[];
  scope?: IdInput;
  generation?: number;
  gen?: number;
  version?: number;
  seq?: number;
  at?: number;
  all?: boolean;
}

export interface MutexGroupSpec {
  id: IdInput;
  channels?: readonly IdInput[];
  frames?: readonly IdInput[];
  members?: readonly IdInput[];
  atomic?: boolean;
  optional?: boolean;
  required?: boolean;
}

export type MutexGroupInput = IdInput | MutexGroupSpec;

export interface SignedEvent {
  type?: string;
  kind?: string;
  op?: string;
  action?: string;
  seq?: number;
  at?: number;
  [key: string]: unknown;
}

export interface MigrationProblem {
  instances?: InstanceCollection;
  connections?: readonly ConnectionSpec[];
  frames?: readonly FrameSpec[];
  channels?: readonly ChannelSpec[];
  queues?: readonly ChannelSpec[];
  messages?: readonly MessageSpec[];
  grants?: readonly GrantSpec[];
  delegations?: readonly GrantSpec[];
  revocations?: readonly RevokeSpec[];
  revokes?: readonly RevokeSpec[];
  mutexGroups?: readonly MutexGroupInput[];
  groups?: readonly MutexGroupInput[];
  events?: readonly SignedEvent[];
  log?: readonly SignedEvent[];
  protectedPublicMessages?: readonly (IdInput | { id?: IdInput; message?: IdInput; messageId?: IdInput })[];
  preservePublicMessages?: readonly (IdInput | { id?: IdInput; message?: IdInput; messageId?: IdInput })[];
  confirmedPublicMessages?: readonly (IdInput | { id?: IdInput; message?: IdInput; messageId?: IdInput })[];
  preserveAllPublicMessages?: boolean;
  requiredFrames?: readonly IdInput[];
  requiredChannels?: readonly IdInput[];
}

export interface MigrationPlan {
  reauthenticate: StableId[];
  continuations: Record<StableId, StableId>;
  frameContinuations: Record<StableId, StableId>;
  stoppedChannels: StableId[];
  stoppedFrames: StableId[];
  stoppedGroups: StableId[];
}

export type PlanInput =
  | MigrationPlan
  | {
      reauthenticate?: readonly IdInput[];
      reauthenticated?: readonly IdInput[];
      reset?: readonly IdInput[];
      reAuth?: readonly IdInput[];
      interrupt?: readonly IdInput[];
      continuations?: Readonly<Record<string, IdInput>>;
      channelContinuations?: Readonly<Record<string, IdInput>>;
      mapping?: Readonly<Record<string, IdInput>>;
      frameContinuations?: Readonly<Record<string, IdInput>>;
      frameMapping?: Readonly<Record<string, IdInput>>;
      stoppedChannels?: readonly IdInput[];
      stopped?: readonly IdInput[];
      interrupted?: readonly IdInput[];
      stoppedFrames?: readonly IdInput[];
      stoppedGroups?: readonly IdInput[];
      stopGroups?: readonly IdInput[];
    }
  | readonly IdInput[];

export interface UnsafeHistoryWitness {
  frame: StableId;
  message: StableId;
  instance: StableId;
}

export interface VerificationWitnesses {
  unsafeHistories: UnsafeHistoryWitness[];
  smallerReset: StableId[] | null;
  lexicographicallySmallerReset: StableId[] | null;
}

export interface VerificationResult {
  ok: boolean;
  valid: boolean;
  safe: boolean;
  preservesPublicMessages: boolean;
  optimal: boolean;
  canonical: boolean;
  errors: string[];
  witnesses: VerificationWitnesses;
  reauthenticate: StableId[];
  continuations: Record<StableId, StableId>;
  frameContinuations: Record<StableId, StableId>;
  stoppedChannels: StableId[];
  stoppedFrames: StableId[];
  stoppedGroups: StableId[];
}

export class MigrationInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MigrationInputError";
  }
}

export class NoFeasiblePlanError extends Error {
  readonly reasons: string[];

  constructor(message: string, reasons: string[] = []) {
    super(message);
    this.name = "NoFeasiblePlanError";
    this.reasons = reasons;
  }
}

export class MigrationVerificationError extends Error {
  readonly result: VerificationResult;

  constructor(result: VerificationResult) {
    super(`Migration plan rejected: ${result.errors.join("; ") || "unknown verification error"}`);
    this.name = "MigrationVerificationError";
    this.result = result;
  }
}
