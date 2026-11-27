import { computeEventHash } from './canonical.ts';
import { signCanonical, signEvent, type MemberKey } from './crypto.ts';
import type {
  AddAliasEvent,
  AddKeyAliasEvent,
  AuthSignature,
  CreateEvent,
  DeleteEvent,
  Envelope,
  ObjectId,
  OwnershipEvent,
  RecoveryEvent,
  RecoveryPolicy,
  RehostEvent,
  RevokeEvent,
  TransferEvent,
  UpdateEvent,
} from './types.ts';

export interface DeliveryInput {
  readonly relay?: string;
  readonly transportSignature?: string;
  readonly arrivedAt: number;
}

function envelope(
  body: OwnershipEvent,
  auth: readonly AuthSignature[],
  delivery: DeliveryInput,
): Envelope {
  return {
    body,
    auth,
    eventHash: computeEventHash(body),
    delivery: { relay: delivery.relay, transportSignature: delivery.transportSignature },
    arrivedAt: delivery.arrivedAt,
  };
}

export function createEnvelope(
  args: {
    id: ObjectId;
    holder: MemberKey;
    recoveryPolicy: RecoveryPolicy;
    displayUrl: string;
    fields?: Record<string, unknown>;
    aliases?: readonly string[];
    parentObject?: ObjectId;
  },
  delivery: DeliveryInput,
): Envelope {
  const body: CreateEvent = {
    type: 'create',
    id: args.id,
    prev: null,
    gen: 0,
    initialHolderKeyId: args.holder.keyId,
    recoveryPolicy: args.recoveryPolicy,
    displayUrl: args.displayUrl,
    fields: args.fields,
    aliases: args.aliases,
    parentObject: args.parentObject,
  };
  return envelope(body, [{ keyId: args.holder.keyId, signature: signEvent(args.holder.privateKeyPem, body) }], delivery);
}

export function updateEnvelope(
  args: { id: ObjectId; prev: string; gen: number; holder: MemberKey; fields: Record<string, unknown> },
  delivery: DeliveryInput,
): Envelope {
  const body: UpdateEvent = {
    type: 'update',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    fields: args.fields,
  };
  return envelope(body, [{ keyId: args.holder.keyId, signature: signEvent(args.holder.privateKeyPem, body) }], delivery);
}

/** 双签转让：旧持有人与接收者分别对同一规范化事件体签名。 */
export function transferEnvelope(
  args: {
    id: ObjectId;
    prev: string;
    gen: number;
    from: MemberKey;
    to: MemberKey;
    displayUrl?: string | null;
  },
  delivery: DeliveryInput,
): Envelope {
  const body: TransferEvent = {
    type: 'transfer',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    fromKeyId: args.from.keyId,
    toKeyId: args.to.keyId,
    displayUrl: args.displayUrl === undefined ? null : args.displayUrl,
  };
  const auth: AuthSignature[] = [
    { keyId: args.from.keyId, signature: signEvent(args.from.privateKeyPem, body) },
    { keyId: args.to.keyId, signature: signEvent(args.to.privateKeyPem, body) },
  ];
  return envelope(body, auth, delivery);
}

export function revokeEnvelope(
  args: {
    id: ObjectId;
    prev: string;
    gen: number;
    holder: MemberKey;
    targetKeyId: string;
    scope: 'edit' | 'all';
  },
  delivery: DeliveryInput,
): Envelope {
  const body: RevokeEvent = {
    type: 'revoke',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    targetKeyId: args.targetKeyId,
    scope: args.scope,
  };
  return envelope(body, [{ keyId: args.holder.keyId, signature: signEvent(args.holder.privateKeyPem, body) }], delivery);
}

export function rehostEnvelope(
  args: { id: ObjectId; prev: string; gen: number; holder: MemberKey; displayUrl: string },
  delivery: DeliveryInput,
): Envelope {
  const body: RehostEvent = {
    type: 'rehost',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    displayUrl: args.displayUrl,
  };
  return envelope(body, [{ keyId: args.holder.keyId, signature: signEvent(args.holder.privateKeyPem, body) }], delivery);
}

export function addAliasEnvelope(
  args: { id: ObjectId; prev: string; gen: number; holder: MemberKey; alias: string },
  delivery: DeliveryInput,
): Envelope {
  const body: AddAliasEvent = {
    type: 'addAlias',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    alias: args.alias,
  };
  return envelope(body, [{ keyId: args.holder.keyId, signature: signEvent(args.holder.privateKeyPem, body) }], delivery);
}

export function addKeyAliasEnvelope(
  args: {
    id: ObjectId;
    prev: string;
    gen: number;
    existing: MemberKey;
    newKey: MemberKey;
  },
  delivery: DeliveryInput,
): Envelope {
  const body: AddKeyAliasEvent = {
    type: 'addKeyAlias',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    existingKeyId: args.existing.keyId,
    newKeyId: args.newKey.keyId,
  };
  return envelope(
    body,
    [{ keyId: args.existing.keyId, signature: signEvent(args.existing.privateKeyPem, body) }],
    delivery,
  );
}

export function deleteEnvelope(
  args: { id: ObjectId; prev: string; gen: number; holder: MemberKey; reason?: string },
  delivery: DeliveryInput,
): Envelope {
  const body: DeleteEvent = {
    type: 'delete',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    reason: args.reason,
  };
  return envelope(body, [{ keyId: args.holder.keyId, signature: signEvent(args.holder.privateKeyPem, body) }], delivery);
}

export function recoveryEnvelope(
  args: {
    id: ObjectId;
    prev: string;
    gen: number;
    forkParent: string;
    branchTips: [string, string];
    canonicalTip: string;
    voters: readonly MemberKey[];
  },
  delivery: DeliveryInput,
): Envelope {
  const base = {
    type: 'recovery',
    id: args.id,
    prev: args.prev,
    gen: args.gen,
    forkParent: args.forkParent,
    branchTips: args.branchTips,
    canonicalTip: args.canonicalTip,
  } as const;
  // 先确定投票成员，再让每名成员对包含完整 votes 的同一规范化事件体签名。
  const claim = { ...base };
  const finalVotes = args.voters.map((member) => ({
    memberKeyId: member.keyId,
    signature: signCanonical(member.privateKeyPem, claim),
  }));
  const finalBody: RecoveryEvent = { ...base, votes: finalVotes };
  // 信封 auth 至少携带一名投票者的授权签名，杜绝纯传输签名提交恢复。
  const first = args.voters[0];
  const auth: AuthSignature[] = first
    ? [{ keyId: first.keyId, signature: signCanonical(first.privateKeyPem, claim) }]
    : [];
  return envelope(finalBody, auth, delivery);
}

/** 重新投递同一原始事件（不同 relay/transport 签名），eventHash 与 auth 完全不变。 */
export function reDeliver(envelope: Envelope, delivery: DeliveryInput): Envelope {
  return {
    ...envelope,
    delivery: { relay: delivery.relay, transportSignature: delivery.transportSignature },
    arrivedAt: delivery.arrivedAt,
  };
}
