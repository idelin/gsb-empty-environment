/**
 * 所有权判定引擎。
 *
 * 核心不变量：
 * 1. 对象身份（永久 ID + 原始签发域）只用于隔离，绝不参与域名排序裁决。
 * 2. 所有权是带精确父节点与代次的 DAG；同一父节点的多个子节点构成分叉。
 * 3. 分叉期间任何字段编辑一律挂起；恢复必须覆盖当前全部活跃末端并满足门槛。
 * 4. 撤销（公钥 / 别名 / 委托）是不可逆墓碑，恢复合并不得复活任何已撤销权利。
 * 5. HTTP 投递签名仅产生参考性证据，永远不能单独授权编辑。
 * 6. 事件乱序到达时，依赖缺失的事件进入挂起缓冲，依赖满足后确定性排空。
 */

import { isUsableEd25519PublicKey, verifySignature } from './signing.ts';
import { hashCanonical, structuredCloneCanonical } from './canonical.ts';
import type {
  ActorId,
  AnyPayload,
  ChainNode,
  ChainNodeId,
  CheckpointSnapshot,
  DomainEvent,
  EditDecision,
  EngineOptions,
  Evidence,
  FieldEditPayload,
  ForkRecoveryPayload,
  IngestResult,
  ObjectId,
  ObjectState,
  OwnershipState,
  PublicKeyInfo,
  QuorumRule,
  Verdict,
} from './types.ts';

/** 可恢复（依赖未就绪）错误：事件转入挂起缓冲而非永久拒绝。 */
class RetryableEngineError extends Error {
  readonly retryable = true as const;
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
    this.name = 'RetryableEngineError';
  }
}

/** 永久错误：事件被拒绝并记录原因。 */
class PermanentEngineError extends Error {
  readonly retryable = false as const;
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
    this.name = 'PermanentEngineError';
  }
}

const defaultQuorum: QuorumRule = (tipCount, distinctSigners) =>
  tipCount > 0 && distinctSigners >= Math.floor(tipCount / 2) + 1;

export class OwnershipEngine {
  readonly state: OwnershipState;
  private readonly quorumRule: QuorumRule;
  private readonly maxPending: number;

  constructor(options: EngineOptions = {}) {
    this.quorumRule = options.quorumRule ?? defaultQuorum;
    this.maxPending = options.maxPending ?? 10_000;
    this.state = {
      objects: {},
      appliedLog: [],
      pending: [],
      rejected: {},
      appliedSeq: 0,
      checkpoints: {},
    };
  }

  // ---------------------------------------------------------------- 读取 API

  /** 返回对象当前状态的深拷贝（外部无法篡改内部状态）。 */
  getObject(objectId: ObjectId): ObjectState | undefined {
    const state = this.state.objects[objectId];
    return state ? structuredCloneCanonical(state) : undefined;
  }

  /** 当前对象是否处于未决分叉（活跃末端超过一个）。 */
  isForked(objectId: ObjectId): boolean {
    const state = this.state.objects[objectId];
    if (!state) {
      return false;
    }
    return this.activeTips(state).length > 1;
  }

  /** 当前所有权末端（未决分叉时返回多个）。 */
  activeTipIds(objectId: ObjectId): ChainNodeId[] {
    const state = this.state.objects[objectId];
    return state ? this.activeTips(state).map((node) => node.nodeId).sort() : [];
  }

  /** 当前规范持有人；分叉未决时为 null（不存在唯一持有人）。 */
  currentOwner(objectId: ObjectId): ActorId | null {
    const state = this.state.objects[objectId];
    if (!state) {
      return null;
    }
    const tips = this.activeTips(state);
    if (tips.length !== 1) {
      return null;
    }
    return tips[0].owner;
  }

  /** 对一个字段编辑事件做纯裁决，不改变引擎状态。 */
  decideEdit(event: DomainEvent<Extract<AnyPayload, { kind: 'field.edit' }>>): EditDecision {
    const payload = event.payload as FieldEditPayload;
    const evidence = this.evaluateEdit(payload, event);
    return { verdict: evidence[evidence.length - 1].verdict, evidence };
  }

  /** 生成对象检查点并在引擎内压缩已应用编辑的载荷骨架。 */
  checkpoint(objectId: ObjectId): CheckpointSnapshot {
    const state = this.requireObject(objectId);
    const throughAppliedSeq = this.state.appliedSeq;
    const stateHash = hashObjectState(state);
    const snapshotState = cloneObjectState(state);
    this.state.checkpoints[objectId] = { throughAppliedSeq, stateHash };
    state.compressedBeforeSeq = throughAppliedSeq;
    for (const edit of state.appliedEdits) {
      edit.compressed = true;
    }
    return { objectId, throughAppliedSeq, stateHash, state: snapshotState };
  }

  // ---------------------------------------------------------------- 摄取入口

  /** 摄取单个事件，自动处理去重、挂起缓冲与依赖排空。 */
  ingest(event: DomainEvent): IngestResult {
    this.validateEnvelope(event);
    const objectId = event.payload.objectId;

    if (this.isKnownEvent(event.eventId)) {
      return {
        eventId: event.eventId,
        objectId,
        status: 'duplicate',
        evidence: [
          evidence('EVENT_DUPLICATE', 'deny', '事件已应用或已拒绝，幂等忽略', objectId, event.eventId),
        ],
      };
    }

    if (this.state.pending.some((candidate) => candidate.eventId === event.eventId)) {
      return {
        eventId: event.eventId,
        objectId,
        status: 'pending',
        evidence: [
          evidence('EVENT_ALREADY_PENDING', 'suspend', '事件已在挂起缓冲中等待前置依赖', objectId, event.eventId),
        ],
      };
    }

    const initial = this.applyWithClassification(event);
    if (initial.status === 'pending') {
      if (this.state.pending.length >= this.maxPending) {
        throw new PermanentEngineError('PENDING_CAPACITY_EXCEEDED', `挂起缓冲达到上限 ${this.maxPending}`);
      }
      this.state.pending.push(event);
      this.drainPending();
    }
    // 只有成功应用（依赖可能被满足）或新挂起事件才触发确定性排空；
    // 永久拒绝不会改变任何依赖关系，排空它只会让本该继续挂起的事件被提前误判。
    if (initial.status === 'applied') {
      this.drainPending();
    }
    this.noteTransport(event, initial);
    return initial;
  }

  /**
   * HTTP 投递层 nonce 重放记账：同一 relay 的传输 nonce 重复出现即留痕拒绝。
   * 注意该检查只描述传输来源，权威裁决仍以所有权链签名为准。
   */
  private noteTransport(event: DomainEvent, result: IngestResult): void {
    const transport = event.transport;
    if (!transport) {
      return;
    }
    const state = this.state.objects[event.payload.objectId];
    if (!state) {
      return;
    }
    const seen = state.transportNonces[transport.relayId] ?? new Set<string>();
    if (seen.has(transport.transportNonce)) {
      result.evidence.push(
        evidence(
          'TRANSPORT_NONCE_REPLAY',
          'deny',
          '检测到重放的 HTTP 投递签名/nonce，仅作传输来源参考且不得用于任何授权',
          event.payload.objectId,
          event.eventId,
          { relayId: transport.relayId },
        ),
      );
      return;
    }
    seen.add(transport.transportNonce);
    state.transportNonces[transport.relayId] = seen;
  }

  /** 批量摄取，返回逐条结果；乱序输入安全。 */
  ingestAll(events: Iterable<DomainEvent>): IngestResult[] {
    return [...events].map((event) => this.ingest(event));
  }

  /**
   * 终结挂起：当调用方确认不会再有新事件到达时，缓冲中剩余的事件
   * 其前置依赖已不可能被满足，统一转为永久拒绝并给出最后一次裁决证据。
   * 这是“乱序收敛”与“非法事件最终拒绝”之间的显式分界。
   */
  settle(): IngestResult[] {
    const results: IngestResult[] = [];
    this.reconcileGrantsToTimeline();
    // 终结阶段：反复“尝试应用 -> 确定性排空”，直到缓冲不再缩小；
    // 仍然残留的事件其依赖在完整事件集下都无法满足，方可永久拒绝。
    let guard = 0;
    while (this.state.pending.length > 0) {
      guard += 1;
      if (guard > this.maxPending + 1) {
        throw new PermanentEngineError('SETTLING_FAILED', '终结排空异常终止，疑似依赖循环');
      }
      const beforeCount = this.state.pending.length;
      this.drainPending();
      if (this.state.pending.length < beforeCount) {
        continue;
      }
      // 无进展：队首事件在完整事件集下仍不可应用，永久拒绝并继续尝试其余事件。
      const [head] = this.state.pending.splice(0, 1);
      const probe = this.applyWithClassification(head);
      const objectId = head.payload.objectId;
      if (probe.status === 'applied') {
        results.push(probe);
        continue;
      }
      const reason = probe.reason ?? 'UNRESOLVABLE_PENDING';
      this.state.rejected[head.eventId] = {
        reason,
        message: probe.evidence.at(-1)?.message ?? '挂起事件在终结阶段仍无法满足依赖',
      };
      this.state.objects[objectId]?.seenEventIds.add(head.eventId);
      this.tombstonePendingGrant(head, reason);
      results.push({
        eventId: head.eventId,
        objectId,
        status: 'rejected',
        verdict: 'deny',
        reason,
        evidence: probe.evidence.map((entry) => ({ ...entry, verdict: 'deny' as const })),
      });
    }
    return results;
  }

  /**
   * 全事件到齐后的时间线复核：任何已应用委托若在其签发时刻不存在
   * 委托者持有的唯一规范末端（即分叉期间越权签发），追溯撤销为墓碑。
   * 这保证乱序路径下“先应用后清除”与“直接拒绝”收敛到同一权威状态。
   */
  private reconcileGrantsToTimeline(): void {
    for (const state of Object.values(this.state.objects)) {
      for (const grant of Object.values(state.grants)) {
        if (grant.status !== 'revoked') {
          const anchor = this.authoritativeTipAt(state, grant.grantedAt, grant.delegator);
          if (!anchor) {
            // 签发时刻处于分叉或委托者非持有人 -> 越权委托，追溯撤销，禁止复活。
            grant.status = 'revoked';
            grant.revokedAt = grant.grantedAt;
            grant.revokedBy = grant.delegator;
          } else if (anchor.nodeId !== grant.anchorNodeId) {
            // 锚点因乱序摄取偏差，统一修正为时间线权威末端，收敛到确定值。
            grant.anchorNodeId = anchor.nodeId;
          }
        }
        if (grant.status === 'revoked') {
          // 墓碑元数据规范化：撤销权利“从未生效”，统一锚定根、撤销时刻归一，
          // 消除“恢复清除”与“终结拒绝”两条乱序路径的记账差异。
          grant.anchorNodeId = state.rootNodeId;
          grant.revokedAt = grant.grantedAt;
          grant.revokedBy = grant.delegator;
        }
      }
    }
  }

  /**
   * 终结阶段被拒的委托事件写入与“恢复清除”同构的墓碑，
   * 使乱序下“拒绝”与“应用后清除”两条路径收敛到相同授权状态。
   */
  private tombstonePendingGrant(event: DomainEvent, reason: string): void {
    if (event.payload.kind !== 'delegation.grant') {
      return;
    }
    const payload = event.payload;
    const state = this.state.objects[payload.objectId];
    if (!state || state.grants[payload.grantId]) {
      return;
    }
    const anchor = this.authoritativeTipAt(state, event.timestamp, payload.delegator);
    state.grants[payload.grantId] = {
      grantId: payload.grantId,
      delegator: payload.delegator,
      delegate: payload.delegate,
      fields: [...new Set(payload.fields)].sort(),
      notBefore: payload.notBefore,
      notAfter: payload.notAfter,
      anchorNodeId: anchor?.nodeId ?? state.rootNodeId,
      status: 'revoked',
      grantedAt: event.timestamp,
      revokedAt: event.timestamp,
      revokedBy: payload.delegator,
    };
    if (!state.seenEventIds.has(event.eventId)) {
      state.seenEventIds.add(event.eventId);
    }
    void reason;
  }

  // ---------------------------------------------------------------- 内部分发

  private applyWithClassification(event: DomainEvent): IngestResult {
    const objectId = event.payload.objectId;
    try {
      const verdict = this.applyEvent(event);
      this.state.appliedSeq += 1;
      this.state.appliedLog.push(structuredCloneCanonical(event));
      const state = this.state.objects[objectId];
      if (state) {
        state.seenEventIds.add(event.eventId);
      }
      const editEvidence = event.payload.kind === 'field.edit' ? this.evaluateEdit(event.payload, event) : [];
      return {
        eventId: event.eventId,
        objectId,
        status: 'applied',
        appliedSeq: this.state.appliedSeq,
        verdict: editEvidence.length ? editEvidence[editEvidence.length - 1].verdict : undefined,
        evidence:
          editEvidence.length > 0
            ? editEvidence
            : [evidence('EVENT_APPLIED', 'allow', `事件 ${event.payload.kind} 已应用`, objectId, event.eventId)],
      };
    } catch (error) {
      if (error instanceof RetryableEngineError) {
        return {
          eventId: event.eventId,
          objectId,
          status: 'pending',
          verdict: 'suspend',
          reason: error.code,
          evidence: [evidence(error.code, 'suspend', error.message, objectId, event.eventId)],
        };
      }
      if (error instanceof PermanentEngineError) {
        this.state.rejected[event.eventId] = { reason: error.code, message: error.message };
        const state = this.state.objects[objectId];
        if (state) {
          state.seenEventIds.add(event.eventId);
        }
        return {
          eventId: event.eventId,
          objectId,
          status: 'rejected',
          verdict: 'deny',
          reason: error.code,
          evidence: [evidence(error.code, 'deny', error.message, objectId, event.eventId)],
        };
      }
      throw error;
    }
  }

  /** 确定性排空：每轮按 (timestamp, eventId) 排序后重试，直到无进展。 */
  private drainPending(): void {
    let progressed = true;
    while (progressed && this.state.pending.length > 0) {
      progressed = false;
      this.state.pending.sort((left, right) =>
        left.timestamp === right.timestamp
          ? left.eventId.localeCompare(right.eventId)
          : left.timestamp - right.timestamp,
      );
      for (let index = 0; index < this.state.pending.length; ) {
        const candidate = this.state.pending[index];
        const result = this.applyWithClassification(candidate);
        if (result.status === 'pending') {
          index += 1;
          continue;
        }
        this.state.pending.splice(index, 1);
        progressed = true;
      }
    }
  }

  private validateEnvelope(event: DomainEvent): void {
    if (!event || typeof event.eventId !== 'string' || event.eventId.length === 0) {
      throw new PermanentEngineError('BAD_ENVELOPE', '事件缺少 eventId');
    }
    if (!event.payload || typeof event.payload.kind !== 'string') {
      throw new PermanentEngineError('BAD_ENVELOPE', '事件缺少 payload.kind');
    }
    if (typeof event.timestamp !== 'number' || !Number.isFinite(event.timestamp)) {
      throw new PermanentEngineError('BAD_ENVELOPE', '事件 timestamp 非法');
    }
  }

  private isKnownEvent(eventId: string): boolean {
    if (this.state.rejected[eventId]) {
      return true;
    }
    for (const state of Object.values(this.state.objects)) {
      if (state.seenEventIds.has(eventId)) {
        return true;
      }
    }
    return false;
  }

  private applyEvent(event: DomainEvent): Verdict | undefined {
    const payload = event.payload;
    switch (payload.kind) {
      case 'object.registration':
        this.applyRegistration(event);
        return undefined;
      case 'key.publish':
        this.applyKeyPublish(event);
        return undefined;
      case 'key.revoke':
        this.applyKeyRevoke(event);
        return undefined;
      case 'alias.bind':
        this.applyAliasBind(event);
        return undefined;
      case 'alias.retire':
        this.applyAliasRetire(event);
        return undefined;
      case 'ownership.transfer':
        this.applyTransfer(event);
        return undefined;
      case 'fork.recovery':
        this.applyRecovery(event);
        return undefined;
      case 'delegation.grant':
        this.applyGrant(event);
        return undefined;
      case 'delegation.revoke':
        this.applyRevoke(event);
        return undefined;
      case 'field.edit':
        this.applyEdit(event);
        return this.evaluateEdit(event.payload, event).at(-1)!.verdict;
      case 'checkpoint.compress':
        this.applyCheckpointMarker(event);
        return undefined;
      default: {
        const exhaustive: never = payload;
        throw new PermanentEngineError('UNKNOWN_EVENT_KIND', `未知事件类型: ${String(exhaustive)}`);
      }
    }
  }

  // ---------------------------------------------------------------- 登记

  private applyRegistration(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'object.registration' }>;
    if (this.state.objects[payload.objectId]) {
      throw new PermanentEngineError(
        'OBJECT_ALREADY_REGISTERED',
        `对象 ${payload.objectId} 已登记，永久 ID 与原始签发域不可变更`,
      );
    }
    if (!payload.originDomain || !payload.permanentId || !payload.initialOwner) {
      throw new PermanentEngineError('BAD_REGISTRATION', '登记缺少 originDomain / permanentId / initialOwner');
    }
    if (!isUsableEd25519PublicKey(payload.initialKey.publicKey)) {
      throw new PermanentEngineError('BAD_INITIAL_KEY', '初始公钥不是可用的 Ed25519 SPKI 密钥');
    }

    const rootNodeId = `root:${payload.objectId}`;
    const state: ObjectState = {
      objectId: payload.objectId,
      originDomain: payload.originDomain,
      permanentId: payload.permanentId,
      rootEventId: event.eventId,
      actors: {
        [payload.initialOwner]: { keys: { [payload.initialKey.keyId]: { ...payload.initialKey } } },
      },
      aliases: {},
      chain: {
        [rootNodeId]: {
          nodeId: rootNodeId,
          kind: 'root',
          owner: payload.initialOwner,
          generation: 0,
          eventId: event.eventId,
          atTime: event.timestamp,
        },
      },
      rootNodeId,
      grants: {},
      appliedEdits: [],
      spentNonces: new Set([payload.nonce]),
      seenEventIds: new Set([event.eventId]),
      transportNonces: {},
    };
    this.state.objects[payload.objectId] = state;
  }

  // ---------------------------------------------------------------- 密钥生命周期

  private applyKeyPublish(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'key.publish' }>;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    const signer = this.ownerKeySigner(state, payload.keyId);
    const signerKey = this.requireActiveKey(state, signer, payload.keyId, event.timestamp);
    this.assertSignature(signerKey, 'key.publish', withoutFields(payload, ['signature']), payload.signature);
    if (!isUsableEd25519PublicKey(payload.key.publicKey)) {
      throw new PermanentEngineError('BAD_PUBLIC_KEY', '新公钥不是可用的 Ed25519 SPKI 密钥');
    }
    const actor = state.actors[payload.actor] ?? { keys: {} };
    state.actors[payload.actor] = actor;
    if (actor.keys[payload.key.keyId]) {
      const existing = actor.keys[payload.key.keyId];
      if (existing.publicKey === payload.key.publicKey) {
        // 幂等：同一 keyId + 同一公钥材料的重复发布（乱序重投或多事件入网）。
        // 版本元数据按确定性归约（最早生效、最晚失效/撤销），与到达顺序无关。
        existing.validFrom = Math.min(existing.validFrom, payload.key.validFrom);
        if (payload.key.validAfter !== undefined) {
          existing.validAfter =
            existing.validAfter === undefined
              ? payload.key.validAfter
              : Math.max(existing.validAfter, payload.key.validAfter);
        }
        return;
      }
      throw new PermanentEngineError('KEY_VERSION_CONFLICT', `公钥版本 ${payload.key.keyId} 已存在且材料不同`);
    }
    actor.keys[payload.key.keyId] = { ...payload.key };
    state.spentNonces.add(payload.nonce);
  }

  /** 由 keyId 约定（actor#version）反推出签名行为体。 */
  private ownerKeySigner(state: ObjectState, keyId: string): ActorId {
    const hashIndex = keyId.indexOf('#');
    if (hashIndex > 0) {
      const candidate = keyId.slice(0, hashIndex);
      if (state.actors[candidate]) {
        return candidate;
      }
    }
    for (const [actor, entry] of Object.entries(state.actors)) {
      if (entry.keys[keyId]) {
        return actor;
      }
    }
    throw new RetryableEngineError('KEY_PENDING', `签名公钥版本 ${keyId} 尚不可见`);
  }

  private applyKeyRevoke(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'key.revoke' }>;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    this.requireCurrentOwner(state, payload.actor);
    const signerKey = this.requireActiveKey(state, payload.actor, payload.keyId, event.timestamp);
    this.assertSignature(signerKey, 'key.revoke', withoutFields(payload, ['signature']), payload.signature);
    const actor = this.requireActor(state, payload.actor);
    const target = actor.keys[payload.revokedKeyId];
    if (!target) {
      throw new RetryableEngineError('KEY_VERSION_MISSING', `待撤销公钥版本 ${payload.revokedKeyId} 尚不可见`);
    }
    if (target.revokedAt !== undefined) {
      throw new PermanentEngineError('KEY_ALREADY_REVOKED', `公钥版本 ${payload.revokedKeyId} 已撤销，撤销不可逆`);
    }
    target.revokedAt = payload.revokedAt;
    state.spentNonces.add(payload.nonce);
  }

  // ---------------------------------------------------------------- 别名

  private applyAliasBind(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'alias.bind' }>;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    this.requireCurrentOwner(state, payload.owner);
    const signerKey = this.requireActiveKey(state, payload.owner, payload.keyId, event.timestamp);
    this.assertSignature(signerKey, 'alias.bind', withoutFields(payload, ['signature']), payload.signature);
    if (!isUsableEd25519PublicKey(payload.aliasKey.publicKey)) {
      throw new PermanentEngineError('BAD_PUBLIC_KEY', '别名公钥不是可用的 Ed25519 SPKI 密钥');
    }
    const existing = state.aliases[payload.alias];
    if (existing?.retiredAt !== undefined) {
      throw new PermanentEngineError('ALIAS_RETIRED', `别名 ${payload.alias} 已退役，禁止重新绑定`);
    }
    if (existing) {
      throw new PermanentEngineError('ALIAS_ALREADY_BOUND', `别名 ${payload.alias} 已绑定`);
    }
    state.aliases[payload.alias] = {
      alias: payload.alias,
      canonical: payload.owner,
      boundAt: event.timestamp,
    };
    const actor = this.requireActor(state, payload.owner);
    if (actor.keys[payload.aliasKey.keyId]) {
      throw new PermanentEngineError('KEY_VERSION_CONFLICT', `公钥版本 ${payload.aliasKey.keyId} 已存在`);
    }
    actor.keys[payload.aliasKey.keyId] = { ...payload.aliasKey };
    // 别名作为独立签名主体登记，解析时映射回规范持有人；
    // 这样别名 keyId（alias#version）可被 findKeyById 直接定位。
    state.actors[payload.alias] = state.actors[payload.alias] ?? { keys: {} };
    state.actors[payload.alias].keys[payload.aliasKey.keyId] = { ...payload.aliasKey };
    state.spentNonces.add(payload.nonce);
  }

  private applyAliasRetire(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'alias.retire' }>;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    this.requireCurrentOwner(state, payload.owner);
    const signerKey = this.requireActiveKey(state, payload.owner, payload.keyId, event.timestamp);
    this.assertSignature(signerKey, 'alias.retire', withoutFields(payload, ['signature']), payload.signature);
    const aliasInfo = state.aliases[payload.alias];
    if (!aliasInfo) {
      throw new RetryableEngineError('ALIAS_MISSING', `别名 ${payload.alias} 尚不可见`);
    }
    if (aliasInfo.canonical !== payload.owner) {
      throw new PermanentEngineError('ALIAS_NOT_OWNED', '只有别名当前所属行为体可以退役别名');
    }
    if (aliasInfo.retiredAt !== undefined) {
      throw new PermanentEngineError('ALIAS_ALREADY_RETIRED', '别名退役不可逆');
    }
    aliasInfo.retiredAt = payload.retiredAt;
    state.spentNonces.add(payload.nonce);
  }

  // ---------------------------------------------------------------- 所有权转让

  private applyTransfer(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'ownership.transfer' }>;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    if (state.chain[payload.transferId]) {
      throw new PermanentEngineError('CHAIN_NODE_CONFLICT', `转让节点 ${payload.transferId} 已存在`);
    }
    const parent = state.chain[payload.parentId];
    if (!parent) {
      throw new RetryableEngineError('PARENT_MISSING', `父转让节点 ${payload.parentId} 尚不可见`);
    }
    if (this.isClosedByRecovery(state, parent.nodeId)) {
      throw new RetryableEngineError('STALE_PARENT', '父节点已被分叉恢复关闭或恢复事件尚未到达，挂起以等待乱序事件');
    }
    if (parent.generation !== payload.prevGeneration) {
      throw new PermanentEngineError(
        'GENERATION_MISMATCH',
        `代次不匹配：父代次 ${parent.generation}，声明 ${payload.prevGeneration}`,
      );
    }
    if (parent.owner !== payload.oldHolder) {
      throw new PermanentEngineError('OLD_HOLDER_MISMATCH', '旧持有人与父节点登记持有人不一致');
    }
    if (payload.oldHolder === payload.newHolder) {
      throw new PermanentEngineError('SELF_TRANSFER', '正式转让的新接收者必须不同于旧持有人');
    }
    const oldKey = this.requireActiveKey(state, payload.oldHolder, payload.oldHolderKeyId, event.timestamp);
    this.assertSignature(
      oldKey,
      'ownership.transfer.old',
      withoutFields(payload, ['oldHolderSignature', 'newHolderSignature']),
      payload.oldHolderSignature,
    );
    const newKey = this.requireKey(state, payload.newHolder, payload.newHolderKeyId);
    this.assertKeyUsableAt(newKey, event.timestamp);
    this.assertSignature(
      newKey,
      'ownership.transfer.new',
      withoutFields(payload, ['oldHolderSignature', 'newHolderSignature']),
      payload.newHolderSignature,
    );

    state.chain[payload.transferId] = {
      nodeId: payload.transferId,
      kind: 'transfer',
      owner: payload.newHolder,
      generation: parent.generation + 1,
      parentId: parent.nodeId,
      eventId: event.eventId,
      atTime: event.timestamp,
    };
    state.spentNonces.add(payload.nonce);
  }

  // ---------------------------------------------------------------- 分叉恢复

  private applyRecovery(event: DomainEvent): void {
    const payload = event.payload as ForkRecoveryPayload;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    if (state.chain[payload.recoveryId]) {
      throw new PermanentEngineError('CHAIN_NODE_CONFLICT', `恢复节点 ${payload.recoveryId} 已存在`);
    }
    if (payload.tipIds.length < 2) {
      throw new PermanentEngineError('RECOVERY_REQUIRES_FORK', '恢复至少需要覆盖两个分叉末端');
    }
    const declaredTips = [...new Set(payload.tipIds)].sort();
    if (declaredTips.length !== payload.tipIds.length) {
      throw new PermanentEngineError('RECOVERY_DUPLICATE_TIP', '恢复末端列表存在重复');
    }
    const activeTips = this.activeTips(state).map((node) => node.nodeId).sort();
    if (declaredTips.length !== activeTips.length || declaredTips.some((tip, index) => tip !== activeTips[index])) {
      throw new RetryableEngineError('RECOVERY_TIP_SET_MISMATCH', '活跃末端集合与声明不一致（可能存在乱序到达的分叉事件），挂起重试', {
        declared: declaredTips,
        active: activeTips,
      });
    }

    const signable = withoutFields(payload, ['quorumSignature']);
    const signers: ActorId[] = [];
    const tipOwners = new Set(activeTips.map((tip) => state.chain[tip].owner));
    for (const endorsement of payload.quorumSignature) {
      const tipNode = activeTips
        .map((tip) => state.chain[tip])
        .find((node) => node.owner === endorsement.actor);
      if (!tipNode) {
        throw new PermanentEngineError('RECOVERY_SIGNER_NOT_TIP_HOLDER', `签名者 ${endorsement.actor} 不是当前末端持有人`);
      }
      if (signers.includes(endorsement.actor)) {
        throw new PermanentEngineError('RECOVERY_DUPLICATE_SIGNER', `末端持有人 ${endorsement.actor} 重复签名`);
      }
      const key = this.requireActiveKey(state, endorsement.actor, endorsement.keyId, event.timestamp);
      this.assertSignature(key, 'fork.recovery', signable, endorsement.signature);
      signers.push(endorsement.actor);
    }
    const everyTipOwnerSigned = [...tipOwners].every((owner) => signers.includes(owner));
    if (!everyTipOwnerSigned) {
      const missing = [...tipOwners].filter((owner) => !signers.includes(owner));
      throw new PermanentEngineError('RECOVERY_SIGNER_NOT_TIP_HOLDER', '存在未签名的活跃末端持有人', {
        missingHolders: missing,
      });
    }
    if (!this.quorumRule(activeTips.length, signers.length)) {
      throw new PermanentEngineError('RECOVERY_QUORUM_UNMET', '法定恢复门槛签名不足', {
        tipCount: activeTips.length,
        signerCount: signers.length,
      });
    }

    const maxGeneration = Math.max(...activeTips.map((tip) => state.chain[tip].generation));
    state.chain[payload.recoveryId] = {
      nodeId: payload.recoveryId,
      kind: 'recovery',
      owner: payload.newHolder,
      generation: maxGeneration + 1,
      recoveryParents: activeTips,
      eventId: event.eventId,
      atTime: event.timestamp,
    };

    // 恢复后的存活委托：仅保留锚定于被合并子 DAG、且在任何分支上都未被撤销的授权。
    for (const grant of Object.values(state.grants)) {
      if (grant.status === 'revoked') {
        continue;
      }
      // 锚点必须是某个活跃末端的“严格祖先”：锚点本身就是分叉末端的授权
      // 属于分叉期间产生的局部权利，恢复后一律清除，不得复活。
      const survives = activeTips.some((tipId) => this.isStrictAncestor(state, grant.anchorNodeId, tipId));
      if (!survives) {
        grant.status = 'revoked';
        grant.revokedAt = event.timestamp;
        grant.revokedBy = payload.newHolder;
      }
    }
    state.spentNonces.add(payload.nonce);
  }

  // ---------------------------------------------------------------- 委托授权

  private applyGrant(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'delegation.grant' }>;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    if (state.grants[payload.grantId]) {
      throw new PermanentEngineError('GRANT_CONFLICT', `委托 ${payload.grantId} 已存在`);
    }
    if (payload.fields.length === 0) {
      throw new PermanentEngineError('GRANT_EMPTY_SCOPE', '委托必须声明至少一个字段作用域');
    }
    if (payload.notBefore >= payload.notAfter) {
      throw new PermanentEngineError('GRANT_BAD_WINDOW', '委托时间窗必须满足 notBefore < notAfter');
    }
    const tips = this.activeTips(state);
    if (tips.length !== 1 || tips[0].owner !== payload.delegator) {
      throw new RetryableEngineError('GRANT_NOT_OWNER', '委托者当前不是唯一规范持有人（分叉或乱序），挂起重试');
    }
    const signerKey = this.requireActiveKey(state, payload.delegator, payload.keyId, event.timestamp);
    this.assertSignature(signerKey, 'delegation.grant', withoutFields(payload, ['signature']), payload.signature);
    this.requireActor(state, payload.delegate);

    // 以委托签发时刻（而非摄取时刻）的链形态确定锚点，保证乱序重放收敛。
    const anchor = this.authoritativeTipAt(state, event.timestamp, payload.delegator);
    if (!anchor) {
      throw new RetryableEngineError('GRANT_NO_ANCHOR_AT_TIME', '签发时刻不存在委托者持有的唯一规范末端（分叉或乱序），挂起重试');
    }
    state.grants[payload.grantId] = {
      grantId: payload.grantId,
      delegator: payload.delegator,
      delegate: payload.delegate,
      fields: [...new Set(payload.fields)].sort(),
      notBefore: payload.notBefore,
      notAfter: payload.notAfter,
      anchorNodeId: anchor.nodeId,
      status: 'active',
      grantedAt: event.timestamp,
    };
    state.spentNonces.add(payload.nonce);
  }

  private applyRevoke(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'delegation.revoke' }>;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    const grant = state.grants[payload.grantId];
    if (!grant) {
      throw new RetryableEngineError('GRANT_MISSING', `委托 ${payload.grantId} 尚不可见`);
    }
    if (grant.status === 'revoked') {
      throw new PermanentEngineError('GRANT_ALREADY_REVOKED', '委托撤销不可逆，禁止重复撤销');
    }
    const tips = this.activeTips(state);
    if (tips.length !== 1 || tips[0].owner !== payload.revoker) {
      throw new RetryableEngineError('REVOKE_NOT_OWNER', '撤销者当前不是唯一规范持有人（分叉或乱序），挂起重试');
    }
    const signerKey = this.requireActiveKey(state, payload.revoker, payload.keyId, event.timestamp);
    this.assertSignature(signerKey, 'delegation.revoke', withoutFields(payload, ['signature']), payload.signature);

    grant.status = 'revoked';
    grant.revokedAt = payload.revokedAt;
    grant.revokedBy = payload.revoker;
    state.spentNonces.add(payload.nonce);
  }

  // ---------------------------------------------------------------- 字段编辑

  private applyEdit(event: DomainEvent): void {
    const payload = event.payload as FieldEditPayload;
    const state = this.requireObject(payload.objectId);
    this.assertFreshNonce(state, payload.nonce);
    if (state.appliedEdits.some((edit) => edit.editId === payload.editId)) {
      throw new PermanentEngineError('EDIT_DUPLICATE', `编辑 ${payload.editId} 已应用`);
    }
    const decision = this.evaluateEdit(payload, event);
    const finalVerdict = decision.at(-1)!.verdict;
    if (finalVerdict === 'deny') {
      const blocking = decision.find((entry) => entry.verdict === 'deny')!;
      throw new PermanentEngineError(blocking.code, blocking.message);
    }
    if (finalVerdict === 'suspend') {
      const blocking = decision.find((entry) => entry.verdict === 'suspend')!;
      throw new RetryableEngineError(blocking.code, blocking.message);
    }
    state.appliedEdits.push({
      editId: payload.editId,
      field: payload.field,
      editor: payload.editor,
      atTime: payload.atTime,
      appliedSeq: this.state.appliedSeq + 1,
    });
    state.spentNonces.add(payload.nonce);
  }

  /**
   * 编辑裁决管线（纯函数）。证据按检查顺序累积，最后一条给出结论。
   * 分叉未决 -> 挂起；任何硬性失败 -> 拒绝；全部通过 -> 允许。
   */
  private evaluateEdit(payload: FieldEditPayload, event: DomainEvent): Evidence[] {
    const trail: Evidence[] = [];
    const objectId = payload.objectId;
    const state = this.state.objects[objectId];
    if (!state) {
      trail.push(evidence('OBJECT_UNKNOWN', 'deny', '对象尚未登记，永久身份无法核验', objectId, event.eventId));
      return trail;
    }

    if (event.transport?.transportSignature) {
      trail.push(
        evidence(
          'TRANSPORT_SIGNATURE_ADVISORY',
          'allow',
          'HTTP 投递签名仅作传输来源参考，不构成编辑授权',
          objectId,
          event.eventId,
          { relayId: event.transport.relayId },
        ),
      );
    }

    const tips = this.activeTips(state);
    if (tips.length > 1) {
      trail.push(
        evidence(
          'FORK_UNRESOLVED',
          'suspend',
          `对象存在 ${tips.length} 个未决分叉末端，局部字段发布挂起直至恢复合并`,
          objectId,
          event.eventId,
          { tipIds: tips.map((node) => node.nodeId).sort() },
        ),
      );
      return trail;
    }

    const aliasInfo = state.aliases[payload.editor];
    let canonicalEditor = payload.editor;
    if (aliasInfo) {
      if (aliasInfo.retiredAt !== undefined) {
        trail.push(
          evidence(
            'ALIAS_RETIRED_DENY',
            'deny',
            `旧别名 ${payload.editor} 已退役，禁止再发布任何局部字段`,
            objectId,
            event.eventId,
          ),
        );
        return trail;
      }
      canonicalEditor = aliasInfo.canonical;
    }

    const editorKey = this.findKeyById(state, payload.editorKeyId);
    if (!editorKey) {
      trail.push(
        evidence('KEY_UNKNOWN', 'suspend', `行为体 ${canonicalEditor} 的公钥版本 ${payload.editorKeyId} 尚不可见，等待密钥事件`, objectId, event.eventId),
      );
      return trail;
    }
    if (editorKey.revokedAt !== undefined) {
      trail.push(evidence('KEY_REVOKED_DENY', 'deny', '签名公钥已撤销，撤销不可逆', objectId, event.eventId));
      return trail;
    }
    if (payload.atTime < editorKey.validFrom || (editorKey.validAfter !== undefined && payload.atTime >= editorKey.validAfter)) {
      trail.push(
        evidence('KEY_OUTSIDE_VALIDITY', 'deny', '签名时间落在该公钥版本的有效区间之外', objectId, event.eventId),
      );
      return trail;
    }
    const signable = withoutFields(payload, ['signature']);
    if (!verifySignature(editorKey.publicKey, 'field.edit', signable, payload.signature)) {
      trail.push(evidence('SIGNATURE_INVALID', 'deny', '编辑负载签名核验失败（重放的传输签名在此必然失效）', objectId, event.eventId));
      return trail;
    }

    const owner = tips[0].owner;
    if (canonicalEditor === owner) {
      trail.push(evidence('OWNER_DIRECT_ALLOW', 'allow', '规范持有人直接签名，字段编辑允许', objectId, event.eventId));
      return trail;
    }

    if (!payload.grantId) {
      trail.push(evidence('NO_DELEGATION', 'deny', '非持有人编辑必须引用有效委托授权', objectId, event.eventId));
      return trail;
    }
    const grant = state.grants[payload.grantId];
    if (!grant) {
      trail.push(evidence('GRANT_UNKNOWN', 'suspend', `委托 ${payload.grantId} 尚不可见，等待授权事件到达`, objectId, event.eventId));
      return trail;
    }
    if (grant.delegate !== canonicalEditor) {
      trail.push(evidence('GRANT_DELEGATE_MISMATCH', 'deny', '编辑者不是该委托的被授权方', objectId, event.eventId));
      return trail;
    }
    if (grant.status === 'revoked') {
      trail.push(
        evidence(
          'GRANT_REVOKED_DENY',
          'deny',
          '委托已撤销且撤销不可逆，分叉恢复也不得复活该编辑权',
          objectId,
          event.eventId,
          { revokedAt: grant.revokedAt, revokedBy: grant.revokedBy },
        ),
      );
      return trail;
    }
    if (payload.atTime < grant.notBefore) {
      // 语义说明：这里挂起针对“授权事件可能乱序、时间窗尚未满足”的场景。
      // 重试由后续事件触发排空；若 atTime 始终早于 notBefore，事件将保持挂起，
      // 不会被错误地永久拒绝，也不会被放行。
      trail.push(evidence('GRANT_NOT_BEFORE', 'suspend', `编辑时间早于委托生效点 ${grant.notBefore}，挂起等待时间窗`, objectId, event.eventId));
      return trail;
    }
    if (payload.atTime >= grant.notAfter) {
      trail.push(
        evidence(
          'GRANT_EXPIRED_DENY',
          'deny',
          `委托已于 ${grant.notAfter} 到期（边界为排他），过期子编辑授权一律拒绝`,
          objectId,
          event.eventId,
        ),
      );
      return trail;
    }
    if (!grant.fields.includes(payload.field)) {
      trail.push(evidence('GRANT_SCOPE_DENY', 'deny', `字段 ${payload.field} 不在委托作用域内`, objectId, event.eventId));
      return trail;
    }
    trail.push(evidence('DELEGATED_ALLOW', 'allow', '委托时间窗、字段作用域与签名均核验通过，字段编辑允许', objectId, event.eventId));
    return trail;
  }

  // ---------------------------------------------------------------- 检查点

  private applyCheckpointMarker(event: DomainEvent): void {
    const payload = event.payload as Extract<AnyPayload, { kind: 'checkpoint.compress' }>;
    const state = this.requireObject(payload.objectId);
    const recomputed = hashObjectState(state);
    if (recomputed !== payload.stateHash) {
      throw new PermanentEngineError(
        'CHECKPOINT_HASH_MISMATCH',
        '检查点状态哈希与无缓存重放结果不一致，拒绝信任压缩点',
        { expected: payload.stateHash, recomputed },
      );
    }
    if (payload.throughAppliedSeq !== this.state.appliedSeq) {
      throw new PermanentEngineError('CHECKPOINT_SEQ_MISMATCH', '检查点应用序号与当前重放进度不一致');
    }
    this.state.checkpoints[payload.objectId] = {
      throughAppliedSeq: payload.throughAppliedSeq,
      stateHash: payload.stateHash,
    };
  }

  // ---------------------------------------------------------------- DAG 辅助

  private activeTips(state: ObjectState): ChainNode[] {
    const referenced = new Set<ChainNodeId>();
    for (const node of Object.values(state.chain)) {
      if (node.parentId) {
        referenced.add(node.parentId);
      }
      for (const parent of node.recoveryParents ?? []) {
        referenced.add(parent);
      }
    }
    return Object.values(state.chain)
      .filter((node) => !referenced.has(node.nodeId))
      .sort((left, right) => left.nodeId.localeCompare(right.nodeId));
  }

  private isClosedByRecovery(state: ObjectState, nodeId: ChainNodeId): boolean {
    return Object.values(state.chain).some(
      (node) => node.kind === 'recovery' && (node.recoveryParents ?? []).includes(nodeId),
    );
  }

  /** 严格祖先：ancestorId 必须位于 nodeId 的上游，且不等于 nodeId 自身。 */
  private isStrictAncestor(state: ObjectState, ancestorId: ChainNodeId, nodeId: ChainNodeId): boolean {
    const current = state.chain[nodeId];
    const parents = [
      ...(current?.parentId ? [current.parentId] : []),
      ...(current?.recoveryParents ?? []),
    ];
    return parents.some((parentId) => this.isAncestor(state, ancestorId, parentId));
  }

  /**
   * 重建给定时刻的所有权末端集合（只取 atTime <= at 的链节点），
   * 用于让委托锚定判定基于事件签发时间而非摄取顺序。
   * 返回该时刻委托者持有的唯一末端；分叉或非持有人时返回 null。
   */
  private authoritativeTipAt(state: ObjectState, at: number, delegator: ActorId): ChainNode | null {
    const visible = Object.values(state.chain).filter((node) => node.atTime <= at);
    const visibleIds = new Set(visible.map((node) => node.nodeId));
    const referenced = new Set<ChainNodeId>();
    for (const node of visible) {
      if (node.parentId && visibleIds.has(node.parentId)) {
        referenced.add(node.parentId);
      }
      for (const parent of node.recoveryParents ?? []) {
        if (visibleIds.has(parent)) {
          referenced.add(parent);
        }
      }
    }
    const tips = visible.filter((node) => !referenced.has(node.nodeId));
    // 签发时刻若已处于分叉（多末端），则不存在唯一规范持有人，委托非法。
    if (tips.length !== 1 || tips[0].owner !== delegator) {
      return null;
    }
    return tips[0];
  }

  private isAncestor(state: ObjectState, ancestorId: ChainNodeId, nodeId: ChainNodeId): boolean {
    const seen = new Set<ChainNodeId>();
    const stack = [nodeId];
    while (stack.length > 0) {
      const currentId = stack.pop()!;
      if (currentId === ancestorId) {
        return true;
      }
      if (seen.has(currentId)) {
        continue;
      }
      seen.add(currentId);
      const current = state.chain[currentId];
      if (!current) {
        continue;
      }
      if (current.parentId) {
        stack.push(current.parentId);
      }
      for (const parent of current.recoveryParents ?? []) {
        stack.push(parent);
      }
    }
    return false;
  }

  // ---------------------------------------------------------------- 状态辅助

  private requireObject(objectId: ObjectId): ObjectState {
    const state = this.state.objects[objectId];
    if (!state) {
      throw new RetryableEngineError('OBJECT_PENDING', `对象 ${objectId} 尚未登记，事件挂起等待登记`);
    }
    return state;
  }

  private requireActor(state: ObjectState, actor: ActorId) {
    const entry = state.actors[actor];
    if (!entry) {
      throw new RetryableEngineError('ACTOR_PENDING', `行为体 ${actor} 在该对象上尚无密钥记录`);
    }
    return entry;
  }

  private findKey(state: ObjectState, actor: ActorId, keyId: string): PublicKeyInfo | undefined {
    return state.actors[actor]?.keys[keyId];
  }

  /** 按 keyId 全局查找（别名密钥登记在规范行为体名下，keyId 自带别名前缀）。 */
  private findKeyById(state: ObjectState, keyId: string): PublicKeyInfo | undefined {
    for (const entry of Object.values(state.actors)) {
      if (entry.keys[keyId]) {
        return entry.keys[keyId];
      }
    }
    return undefined;
  }

  private requireKey(state: ObjectState, actor: ActorId, keyId: string): PublicKeyInfo {
    const key = this.findKey(state, actor, keyId);
    if (!key) {
      throw new RetryableEngineError('KEY_PENDING', `行为体 ${actor} 的公钥版本 ${keyId} 尚不可见`);
    }
    return key;
  }

  private requireActiveKey(state: ObjectState, actor: ActorId, keyId: string, atTime: number): PublicKeyInfo {
    const key = this.requireKey(state, actor, keyId);
    this.assertKeyUsableAt(key, atTime);
    return key;
  }

  private assertKeyUsableAt(key: PublicKeyInfo, atTime: number): void {
    if (key.revokedAt !== undefined) {
      throw new PermanentEngineError('KEY_ALREADY_REVOKED', `公钥版本 ${key.keyId} 已撤销，撤销不可逆`);
    }
    if (atTime < key.validFrom || (key.validAfter !== undefined && atTime >= key.validAfter)) {
      throw new PermanentEngineError('KEY_OUTSIDE_VALIDITY', `公钥版本 ${key.keyId} 在 ${atTime} 时不在有效区间`);
    }
  }

  private requireCurrentOwner(state: ObjectState, actor: ActorId): void {
    const tips = this.activeTips(state);
    if (tips.length > 1) {
      throw new RetryableEngineError('FORK_UNRESOLVED', '分叉未决期间不存在唯一规范持有人，管理操作挂起');
    }
    if (tips[0].owner !== actor) {
      throw new RetryableEngineError('NOT_CURRENT_OWNER', `行为体 ${actor} 当前不是规范持有人（可能由乱序到达导致），挂起重试`);
    }
  }

  private assertFreshNonce(state: ObjectState | undefined, nonce: string): void {
    if (!nonce) {
      throw new PermanentEngineError('NONCE_MISSING', '事件缺少防重放 nonce');
    }
    if (state?.spentNonces.has(nonce)) {
      throw new PermanentEngineError('NONCE_REPLAY', `nonce ${nonce} 已在该对象上使用，重放被拒绝`);
    }
  }

  private assertSignature(key: PublicKeyInfo, purpose: string, payload: unknown, signature: string): void {
    if (!signature || !verifySignature(key.publicKey, purpose, payload, signature)) {
      throw new PermanentEngineError('SIGNATURE_INVALID', `${purpose} 签名核验失败`);
    }
  }
}

// ---------------------------------------------------------------- 模块级工具

export function evidence(
  code: string,
  verdict: Verdict,
  message: string,
  objectId: ObjectId,
  eventId?: string,
  detail?: Readonly<Record<string, unknown>>,
): Evidence {
  return { code, verdict, message, objectId, eventId, detail };
}

/** 构造签名负载时剔除签名自身字段，保证签名消息不循环依赖。 */
export function withoutFields<T extends object>(payload: T, keys: ReadonlyArray<string>): Record<string, unknown> {
  const clone = structuredCloneCanonical(payload) as Record<string, unknown>;
  for (const key of keys) {
    delete clone[key];
  }
  return clone;
}

function sortRecord<T>(
  record: Readonly<Record<string, T>>,
  mapper: (entry: T) => unknown,
): Array<[string, unknown]> {
  return Object.keys(record)
    .sort()
    .map((key) => [key, mapper(record[key])]);
}

/** 单对象状态的确定性哈希（集合先排序，保证跨进程一致）。 */
export function hashObjectState(state: ObjectState): string {
  return hashCanonical(buildHashView(state));
}

/** 构造参与语义哈希的规范化视图（压缩标记等存储优化不参与）。 */
export function buildHashView(state: ObjectState): Record<string, unknown> {
  return {
    objectId: state.objectId,
    originDomain: state.originDomain,
    permanentId: state.permanentId,
    rootEventId: state.rootEventId,
    rootNodeId: state.rootNodeId,
    actors: sortRecord(state.actors, (entry) => ({
      keys: sortRecord(entry.keys, (key) => ({ ...key })),
    })),
    aliases: sortRecord(state.aliases, (alias) => ({ ...alias })),
    chain: sortRecord(state.chain, (node) => ({
      ...node,
      recoveryParents: [...(node.recoveryParents ?? [])].sort(),
    })),
    grants: sortRecord(state.grants, (grant) => ({ ...grant, fields: [...grant.fields].sort() })),
    appliedEdits: [...state.appliedEdits]
      .sort((left, right) => left.editId.localeCompare(right.editId))
      .map(({ editId, field, editor, atTime }) => ({ editId, field, editor, atTime })),
    // nonce / seenEvent / transport 记账是摄取过程的防重放元数据，
    // 同一权威状态在不同乱序路径下记账形态可能不同，故不参与语义收敛哈希。
  };
}

/** JSON 往返后 Set 会退化为对象，这里统一兼容两种形态。 */
function toArray<T>(value: ReadonlySet<T> | Readonly<Record<string, T>> | T[]): T[] {
  if (value instanceof Set || Array.isArray(value)) {
    return [...value];
  }
  return Object.values(value);
}

/** 深拷贝单对象状态并完整保留 Set 语义（JSON 原生会把 Set 序列化为空对象）。 */
export function cloneObjectState(state: ObjectState): ObjectState {
  return {
    ...structuredCloneCanonical(state),
    spentNonces: new Set(state.spentNonces),
    seenEventIds: new Set(state.seenEventIds),
    transportNonces: Object.fromEntries(
      Object.entries(state.transportNonces).map(([relay, set]) => [relay, new Set(set)]),
    ),
  };
}
