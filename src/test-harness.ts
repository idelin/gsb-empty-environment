/**
 * 测试与服务端引导辅助：集中托管 Ed25519 密钥并构造已签名事件。
 * 生产服务端可改用 HSM / KMS 实现同样的 signMessage 约定。
 */

import { generateSigningKey, signMessage } from './signing.ts';
import { withoutFields } from './engine.ts';
import type {
  ActorId,
  AliasBindPayload,
  ChainNodeId,
  CheckpointCompressPayload,
  DelegationGrantPayload,
  DelegationRevokePayload,
  DomainEvent,
  FieldEditPayload,
  ForkRecoveryPayload,
  KeyPublishPayload,
  KeyRevokePayload,
  ObjectId,
  ObjectRegistrationPayload,
  OwnershipTransferPayload,
  PublicKeyInfo,
  SigningKey,
  TransportEnvelope,
} from './types.ts';

let eventSequence = 0;

export function nextEventId(prefix = 'evt'): string {
  eventSequence += 1;
  return `${prefix}-${eventSequence.toString(16).padStart(4, '0')}`;
}

export function resetEventSequence(): void {
  eventSequence = 0;
}

export class KeyWallet {
  private readonly keys = new Map<string, SigningKey>();

  actor(actor: ActorId, keyId = `${actor}#k1`): SigningKey {
    const cacheKey = `${actor}:${keyId}`;
    const existing = this.keys.get(cacheKey);
    if (existing) {
      return existing;
    }
    const created = generateSigningKey(keyId);
    this.keys.set(cacheKey, created);
    return created;
  }

  keyInfo(actor: ActorId, keyId = `${actor}#k1`, validFrom = 0): PublicKeyInfo {
    const key = this.actor(actor, keyId);
    return { keyId, publicKey: key.publicKeySpki, validFrom };
  }

  sign(actor: ActorId, keyId: string, purpose: string, payload: unknown): string {
    const key = this.keys.get(`${actor}:${keyId}`);
    if (!key) {
      throw new Error(`钱包中缺少 ${actor}/${keyId} 的私钥`);
    }
    return signMessage(key.privateKeyPkcs8, purpose, payload);
  }
}

function envelope<P>(payload: P, timestamp: number, transport?: TransportEnvelope): DomainEvent & { payload: P } {
  return { eventId: nextEventId(), timestamp, payload, ...(transport ? { transport } : {}) };
}

export class EventFactory {
  readonly wallet = new KeyWallet();

  registration(
    objectId: ObjectId,
    originDomain: string,
    owner: ActorId,
    timestamp: number,
    permanentId = `permid:${objectId}`,
  ): DomainEvent & { payload: ObjectRegistrationPayload } {
    const initialKey = this.wallet.keyInfo(owner, `${owner}#k1`, 0);
    const payload: ObjectRegistrationPayload = {
      kind: 'object.registration',
      objectId,
      originDomain,
      permanentId,
      initialOwner: owner,
      initialKey,
      nonce: `n-${nextEventId()}`,
    };
    return envelope(payload, timestamp);
  }

  keyPublish(
    objectId: ObjectId,
    actor: ActorId,
    newKeyId: string,
    timestamp: number,
    signer: ActorId = actor,
    signerKeyId = `${signer}#k1`,
  ): DomainEvent & { payload: KeyPublishPayload } {
    this.wallet.actor(actor, newKeyId);
    const key = this.wallet.keyInfo(actor, newKeyId, timestamp);
    const unsigned = {
      kind: 'key.publish' as const,
      objectId,
      actor,
      key,
      nonce: `n-${nextEventId()}`,
      keyId: signerKeyId,
      signature: '',
    };
    unsigned.signature = this.wallet.sign(signer, signerKeyId, 'key.publish', withoutFields(unsigned, ['signature']));
    return envelope(unsigned, timestamp);
  }

  /**
   * 新行为体入网：由当前持有人背书发布该行为体的初始公钥版本。
   * 转让的新接收者必须先完成入网，转让双签名才可核验。
   */
  onboardActor(
    objectId: ObjectId,
    actor: ActorId,
    timestamp: number,
    sponsor: ActorId,
    keyId = `${actor}#k1`,
  ): DomainEvent & { payload: KeyPublishPayload } {
    return this.keyPublish(objectId, actor, keyId, timestamp, sponsor, `${sponsor}#k1`);
  }

  keyRevoke(objectId: ObjectId, actor: ActorId, revokedKeyId: string, timestamp: number, signerKeyId = `${actor}#k1`): DomainEvent & { payload: KeyRevokePayload } {
    const unsigned = {
      kind: 'key.revoke' as const,
      objectId,
      actor,
      revokedKeyId,
      revokedAt: timestamp,
      nonce: `n-${nextEventId()}`,
      keyId: signerKeyId,
      signature: '',
    };
    unsigned.signature = this.wallet.sign(actor, signerKeyId, 'key.revoke', withoutFields(unsigned, ['signature']));
    return envelope(unsigned, timestamp);
  }

  aliasBind(objectId: ObjectId, owner: ActorId, alias: ActorId, timestamp: number): DomainEvent & { payload: AliasBindPayload } {
    const aliasKey = this.wallet.keyInfo(alias, `${alias}#ak1`, timestamp);
    const unsigned = {
      kind: 'alias.bind' as const,
      objectId,
      owner,
      alias,
      aliasKey,
      nonce: `n-${nextEventId()}`,
      keyId: `${owner}#k1`,
      signature: '',
    };
    unsigned.signature = this.wallet.sign(owner, unsigned.keyId, 'alias.bind', withoutFields(unsigned, ['signature']));
    return envelope(unsigned, timestamp);
  }

  aliasRetire(objectId: ObjectId, owner: ActorId, alias: ActorId, timestamp: number): DomainEvent & { payload: import('./types.ts').AliasRetirePayload } {
    const unsigned = {
      kind: 'alias.retire' as const,
      objectId,
      owner,
      alias,
      retiredAt: timestamp,
      nonce: `n-${nextEventId()}`,
      keyId: `${owner}#k1`,
      signature: '',
    };
    unsigned.signature = this.wallet.sign(owner, unsigned.keyId, 'alias.retire', withoutFields(unsigned, ['signature']));
    return envelope(unsigned, timestamp);
  }

  transfer(
    objectId: ObjectId,
    parentId: ChainNodeId,
    prevGeneration: number,
    oldHolder: ActorId,
    newHolder: ActorId,
    timestamp: number,
    transferId?: ChainNodeId,
  ): DomainEvent & { payload: OwnershipTransferPayload } {
    const oldHolderKeyId = `${oldHolder}#k1`;
    const newHolderKeyId = `${newHolder}#k1`;
    // 新接收者密钥材料仅用于签名；其公钥必须先经 keyPublish 对对象可见，
    // 否则引擎会以 KEY_PENDING 挂起转让（调用方负责补发密钥发布事件）。
    this.wallet.actor(newHolder, newHolderKeyId);
    const unsigned = {
      kind: 'ownership.transfer' as const,
      objectId,
      transferId: transferId ?? `tx:${nextEventId()}`,
      parentId,
      prevGeneration,
      oldHolder,
      newHolder,
      nonce: `n-${nextEventId()}`,
      oldHolderKeyId,
      newHolderKeyId,
      oldHolderSignature: '',
      newHolderSignature: '',
    };
    const signable = withoutFields(unsigned, ['oldHolderSignature', 'newHolderSignature']);
    unsigned.oldHolderSignature = this.wallet.sign(oldHolder, oldHolderKeyId, 'ownership.transfer.old', signable);
    unsigned.newHolderSignature = this.wallet.sign(newHolder, newHolderKeyId, 'ownership.transfer.new', signable);
    return envelope(unsigned, timestamp);
  }

  recovery(
    objectId: ObjectId,
    tipIds: ReadonlyArray<ChainNodeId>,
    tipHolders: ReadonlyArray<ActorId>,
    newHolder: ActorId,
    timestamp: number,
  ): DomainEvent & { payload: ForkRecoveryPayload } {
    this.wallet.actor(newHolder, `${newHolder}#k1`);
    const unsigned = {
      kind: 'fork.recovery' as const,
      objectId,
      recoveryId: `rc:${nextEventId()}`,
      tipIds: [...tipIds],
      newHolder,
      quorumSignature: [] as ForkRecoveryPayload['quorumSignature'],
      nonce: `n-${nextEventId()}`,
    };
    const signable = withoutFields(unsigned, ['quorumSignature']);
    unsigned.quorumSignature = tipHolders.map((actor) => {
      const keyId = `${actor}#k1`;
      return {
        actor,
        keyId,
        signature: this.wallet.sign(actor, keyId, 'fork.recovery', signable),
      };
    });
    return envelope(unsigned, timestamp);
  }

  grant(
    objectId: ObjectId,
    delegator: ActorId,
    delegate: ActorId,
    fields: ReadonlyArray<string>,
    notBefore: number,
    notAfter: number,
    timestamp: number,
    grantId?: string,
  ): { onboarding: DomainEvent & { payload: KeyPublishPayload }; grant: DomainEvent & { payload: DelegationGrantPayload } } {
    this.wallet.actor(delegate, `${delegate}#k1`);
    const onboarding = this.onboardActor(objectId, delegate, timestamp, delegator);
    const unsigned = {
      kind: 'delegation.grant' as const,
      objectId,
      grantId: grantId ?? `grant:${nextEventId()}`,
      delegator,
      delegate,
      fields: [...fields],
      notBefore,
      notAfter,
      nonce: `n-${nextEventId()}`,
      keyId: `${delegator}#k1`,
      signature: '',
    };
    unsigned.signature = this.wallet.sign(delegator, unsigned.keyId, 'delegation.grant', withoutFields(unsigned, ['signature']));
    return { onboarding, grant: envelope(unsigned, timestamp) };
  }

  revoke(objectId: ObjectId, grantId: string, revoker: ActorId, timestamp: number): DomainEvent & { payload: DelegationRevokePayload } {
    const unsigned = {
      kind: 'delegation.revoke' as const,
      objectId,
      grantId,
      revoker,
      revokedAt: timestamp,
      nonce: `n-${nextEventId()}`,
      keyId: `${revoker}#k1`,
      signature: '',
    };
    unsigned.signature = this.wallet.sign(revoker, unsigned.keyId, 'delegation.revoke', withoutFields(unsigned, ['signature']));
    return envelope(unsigned, timestamp);
  }

  edit(
    objectId: ObjectId,
    editor: ActorId,
    editorKeyId: string,
    field: string,
    value: unknown,
    atTime: number,
    options: { grantId?: string; transport?: TransportEnvelope } = {},
  ): DomainEvent & { payload: FieldEditPayload } {
    this.wallet.actor(editor, editorKeyId);
    const unsigned = {
      kind: 'field.edit' as const,
      objectId,
      editId: `edit:${nextEventId()}`,
      editor,
      editorKeyId,
      field,
      value,
      atTime,
      ...(options.grantId ? { grantId: options.grantId } : {}),
      nonce: `n-${nextEventId()}`,
      signature: '',
    };
    unsigned.signature = this.wallet.sign(editor, editorKeyId, 'field.edit', withoutFields(unsigned, ['signature']));
    return envelope(unsigned, atTime, options.transport);
  }

  checkpoint(objectId: ObjectId, throughAppliedSeq: number, stateHash: string, timestamp: number): DomainEvent & { payload: CheckpointCompressPayload } {
    return envelope(
      {
        kind: 'checkpoint.compress',
        objectId,
        throughAppliedSeq,
        stateHash,
      },
      timestamp,
    );
  }
}
