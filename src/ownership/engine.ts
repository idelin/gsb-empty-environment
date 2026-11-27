import { checkAuthorization, hasSigner } from './auth.ts';
import { verifyCanonical } from './crypto.ts';
import { descendantKeys, type EventNode, type InboxGraph, type ObjectGraph } from './graph.ts';
import type {
  Checkpoint,
  DecisionKind,
  DecisionReason,
  EventDecision,
  KeyResolver,
  ObjectView,
  RecoveryEvent,
  TransferEvent,
} from './types.ts';
import { objectIdKey } from './types.ts';

interface TransferState {
  hash: string;
  gen: number;
  holder: string;
  deleted: boolean;
}

interface Fork {
  parentHash: string;
  branches: Set<string>;
  resolvedBy: string | null;
  canonicalTip: string | null;
}

interface ObjectRuntime {
  graph: ObjectGraph;
  checkpoint: Checkpoint | null;
  policy: Checkpoint['recoveryPolicy'] | null;
  transfers: Map<string, TransferState>;
  openForks: Map<string, Fork>;
  authority: string[];
  aliases: Set<string>;
  keyAliases: Map<string, Set<string>>;
  revokedEdit: Set<string>;
  revokedAll: Set<string>;
  displayUrl: string | null;
  holder: string | null;
  currentTip: string | null;
  currentGen: number;
  deleted: boolean;
  forked: boolean;
  rootHash: string | null;
}

export interface InternalDecision {
  eventHash: string;
  kind: DecisionKind;
  reason: DecisionReason;
  awaiting: string[];
}

export interface EngineResult {
  decisions: InternalDecision[];
  views: Map<string, ObjectView>;
  isolated: string[];
}

export function refOf(node: EventNode): EventDecision['ref'] {
  return { eventHash: node.hash, prev: node.body.prev, gen: node.body.gen };
}

function makeDecision(
  node: EventNode,
  kind: DecisionKind,
  reason: DecisionReason,
  awaiting: string[] = [],
): InternalDecision {
  return { eventHash: node.hash, kind, reason, awaiting };
}

function keyEquivalent(rt: ObjectRuntime, candidate: string, anchor: string): boolean {
  if (candidate === anchor) return true;
  const seen = new Set<string>([anchor]);
  const queue = [anchor];
  while (queue.length > 0) {
    const current = queue.pop()!;
    const aliases = rt.keyAliases.get(current);
    if (!aliases) continue;
    for (const alias of aliases) {
      if (alias === candidate) return true;
      if (!seen.has(alias)) {
        seen.add(alias);
        queue.push(alias);
      }
    }
  }
  return false;
}

export class OwnershipEngine {
  readonly #resolver: KeyResolver;
  readonly #checkpoints: Map<string, Checkpoint>;

  constructor(resolver: KeyResolver, checkpoints: readonly Checkpoint[] = []) {
    this.#resolver = resolver;
    this.#checkpoints = new Map(checkpoints.map((cp) => [objectIdKey(cp.object), cp]));
  }

  run(inbox: InboxGraph): EngineResult {
    const runtimes = new Map<string, ObjectRuntime>();
    const decisions: InternalDecision[] = [];

    for (const [key, graph] of inbox.objects) {
      const rt = this.#initRuntime(key, graph);
      runtimes.set(key, rt);
      this.#evaluateObject(rt, decisions);
    }

    const isolated = new Set<string>();
    for (const [key, rt] of runtimes) {
      if (rt.forked) {
        isolated.add(key);
        for (const descendant of descendantKeys(inbox, key)) isolated.add(descendant);
      }
    }
    this.#applyDescendantIsolation(runtimes, isolated, decisions);
    const views = this.#buildViews(inbox, runtimes, isolated);
    return { decisions, views, isolated: [...isolated].sort() };
  }

  #initRuntime(key: string, graph: ObjectGraph): ObjectRuntime {
    const checkpoint = this.#checkpoints.get(key) ?? null;
    return {
      graph,
      checkpoint,
      policy: checkpoint?.recoveryPolicy ?? null,
      transfers: new Map(),
      openForks: new Map(),
      authority: checkpoint ? [checkpoint.tipHash] : [],
      aliases: new Set(checkpoint?.aliases ?? []),
      keyAliases: new Map(),
      revokedEdit: new Set(checkpoint?.revokedEdit ?? []),
      revokedAll: new Set(checkpoint?.revokedAll ?? []),
      displayUrl: checkpoint?.displayUrl ?? null,
      holder: checkpoint?.holderKeyId ?? null,
      currentTip: checkpoint?.tipHash ?? null,
      currentGen: checkpoint?.gen ?? 0,
      deleted: checkpoint?.deleted ?? false,
      forked: false,
      rootHash: graph.root,
    };
  }

  #evaluateObject(rt: ObjectRuntime, decisions: InternalDecision[]): void {
    const graph = rt.graph;
    if (graph.root) {
      this.#handleCreate(rt, graph.nodes.get(graph.root)!, decisions);
    } else if (!rt.checkpoint) {
      for (const node of graph.nodes.values()) {
        decisions.push(makeDecision(node, 'reject', 'reject_unknown_object'));
      }
      return;
    } else {
      this.#seedCheckpoint(rt);
    }

    const settled = new Set<string>();
    if (rt.rootHash) settled.add(rt.rootHash);
    let guard = 0;
    const maxGuards = graph.nodes.size * 6 + 16;
    let progressed = true;
    while (progressed && guard < maxGuards) {
      guard += 1;
      progressed = false;
      const pending = [...graph.nodes.values()]
        .filter((node) => !settled.has(node.hash) && node.body.type !== 'create')
        .sort((a, b) =>
          a.body.gen !== b.body.gen ? a.body.gen - b.body.gen : a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0,
        );
      for (const node of pending) {
        const decisionCount = decisions.length;
        const outcome = this.#evaluateNode(rt, node, decisions);
        if (outcome === 'settled') {
          settled.add(node.hash);
          progressed = true;
        } else if (decisions.length !== decisionCount) {
          progressed = true;
        }
      }
    }

    for (const node of graph.nodes.values()) {
      if (settled.has(node.hash) || node.body.type === 'create') continue;
      if (decisions.some((d) => d.eventHash === node.hash)) continue;
      this.#markWaiting(rt, node, decisions);
    }
    this.#project(rt, decisions);
  }

  #seedCheckpoint(rt: ObjectRuntime): void {
    const cp = rt.checkpoint!;
    rt.transfers.set(cp.tipHash, {
      hash: cp.tipHash,
      gen: cp.gen,
      holder: cp.holderKeyId,
      deleted: cp.deleted,
    });
    rt.holder = cp.holderKeyId;
  }

  #handleCreate(rt: ObjectRuntime, node: EventNode, decisions: InternalDecision[]): void {
    const body = node.body;
    if (body.type !== 'create') return;
    const auth = checkAuthorization(body, node.auth, this.#resolver);
    if (!auth.ok) {
      decisions.push(
        makeDecision(node, auth.reason === 'wait_unknown_key' ? 'wait' : 'reject', auth.reason, [
          ...auth.unknownKeyIds,
        ]),
      );
      return;
    }
    if (!hasSigner(node.auth, body.initialHolderKeyId)) {
      decisions.push(makeDecision(node, 'reject', 'reject_unauthorized_signer'));
      return;
    }
    rt.policy = body.recoveryPolicy;
    rt.holder = body.initialHolderKeyId;
    rt.currentTip = node.hash;
    rt.currentGen = 0;
    rt.displayUrl = body.displayUrl;
    for (const alias of body.aliases ?? []) rt.aliases.add(alias);
    rt.transfers.set(node.hash, {
      hash: node.hash,
      gen: 0,
      holder: body.initialHolderKeyId,
      deleted: false,
    });
    if (rt.checkpoint) this.#seedCheckpoint(rt);
    rt.authority = [node.hash];
    decisions.push(makeDecision(node, 'publish', 'ok'));
  }

  #evaluateNode(rt: ObjectRuntime, node: EventNode, decisions: InternalDecision[]): 'settled' | 'waiting' {
    const body = node.body;
    if (body.type === 'create') return 'settled';

    // recovery 的授权来自 votes 内部逐票校验；信封仍须携带至少一条授权签名，
    // 杜绝仅凭重放的 HTTP 传输签名提交恢复。
    if (body.type !== 'recovery') {
      const auth = checkAuthorization(body, node.auth, this.#resolver);
      if (!auth.ok) {
        if (auth.reason === 'wait_unknown_key') return 'waiting';
        decisions.push(makeDecision(node, 'reject', auth.reason));
        return 'settled';
      }
    } else if (!node.auth || node.auth.length === 0) {
      decisions.push(makeDecision(node, 'reject', 'reject_transport_only'));
      return 'settled';
    }

    if (body.type === 'transfer') return this.#handleTransfer(rt, node, decisions);
    if (body.type === 'recovery') return this.#handleRecovery(rt, node, decisions);
    return this.#handleMutation(rt, node, decisions);
  }

  #parentTransferHash(rt: ObjectRuntime, transferHash: string): string | null {
    return rt.graph.nodes.get(transferHash)?.body.prev ?? null;
  }

  #handleTransfer(rt: ObjectRuntime, node: EventNode, decisions: InternalDecision[]): 'settled' | 'waiting' {
    const body = node.body as TransferEvent;
    const parent = body.prev ? rt.transfers.get(body.prev) : undefined;
    if (!parent) return 'waiting';
    if (body.gen !== parent.gen + 1) {
      decisions.push(makeDecision(node, 'reject', 'reject_stale_gen'));
      return 'settled';
    }
    const signerIds = node.auth.map((entry) => entry.keyId);
    const oldOk = signerIds.some((keyId) => keyEquivalent(rt, keyId, parent.holder));
    const newOk = signerIds.some((keyId) => keyEquivalent(rt, keyId, body.toKeyId));
    if (!oldOk || !newOk || body.fromKeyId !== parent.holder) {
      decisions.push(makeDecision(node, 'reject', 'reject_unauthorized_signer'));
      return 'settled';
    }
    if (parent.deleted) {
      decisions.push(makeDecision(node, 'reject', 'reject_already_deleted'));
      return 'settled';
    }

    const state: TransferState = {
      hash: node.hash,
      gen: body.gen,
      holder: body.toKeyId,
      deleted: false,
    };
    rt.transfers.set(node.hash, state);

    const forkParent = body.prev!;
    const siblingHashes = [...rt.transfers.keys()].filter(
      (hash) =>
        hash !== node.hash &&
        this.#parentTransferHash(rt, hash) === forkParent &&
        rt.transfers.get(hash)!.gen === body.gen,
    );
    const existingFork = rt.openForks.get(forkParent);
    if (siblingHashes.length > 0 || existingFork) {
      const fork =
        existingFork ??
        ({
          parentHash: forkParent,
          branches: new Set(siblingHashes),
          resolvedBy: null,
          canonicalTip: null,
        } satisfies Fork);
      fork.branches.add(node.hash);
      rt.openForks.set(forkParent, fork);
      rt.forked = true;
      decisions.push(
        makeDecision(node, 'wait', 'wait_fork_unresolved', [
          `recovery covering forkParent=${forkParent}`,
        ]),
      );
      return 'settled';
    }

    if (rt.currentTip === body.prev && !rt.forked) {
      rt.currentTip = node.hash;
      rt.currentGen = body.gen;
      rt.holder = body.toKeyId;
      if (body.displayUrl !== null) rt.displayUrl = body.displayUrl;
      rt.authority.push(node.hash);
      decisions.push(makeDecision(node, 'publish', 'ok'));
      return 'settled';
    }
    return 'waiting';
  }

  #handleMutation(rt: ObjectRuntime, node: EventNode, decisions: InternalDecision[]): 'settled' | 'waiting' {
    const body = node.body;
    if (
      body.type === 'create' ||
      body.type === 'transfer' ||
      body.type === 'recovery'
    ) {
      return 'waiting';
    }
    const anchor = body.prev ? rt.transfers.get(body.prev) : undefined;
    if (!anchor) return 'waiting';
    if (body.gen !== anchor.gen) {
      decisions.push(makeDecision(node, 'reject', 'reject_stale_gen'));
      return 'settled';
    }

    const signer = node.auth[0]?.keyId;
    const signerIsHolder = signer !== undefined && keyEquivalent(rt, signer, anchor.holder);
    if (!signerIsHolder) {
      decisions.push(makeDecision(node, 'reject', 'reject_unauthorized_signer'));
      return 'settled';
    }
    if (body.type !== 'revoke' && (rt.revokedEdit.has(signer) || rt.revokedAll.has(signer))) {
      decisions.push(makeDecision(node, 'reject', 'reject_revoked_editor'));
      return 'settled';
    }
    if (anchor.deleted) {
      decisions.push(makeDecision(node, 'reject', 'reject_already_deleted'));
      return 'settled';
    }
    if (body.type === 'addAlias' && rt.aliases.has(body.alias)) {
      decisions.push(makeDecision(node, 'reject', 'reject_alias_taken'));
      return 'settled';
    }
    if (body.type === 'addKeyAlias' && !keyEquivalent(rt, signer, body.existingKeyId)) {
      decisions.push(makeDecision(node, 'reject', 'reject_unauthorized_signer'));
      return 'settled';
    }

    // 身份/权限元数据（撤回、密钥别名）即使在分叉期也即时累积，
    // 因为恢复门槛与"不复活撤回"依赖它们；但它们不作为数据字段发布。
    if (body.type === 'revoke') {
      if (body.scope === 'all') {
        rt.revokedAll.add(body.targetKeyId);
        rt.revokedEdit.add(body.targetKeyId);
      } else {
        rt.revokedEdit.add(body.targetKeyId);
      }
    } else if (body.type === 'addKeyAlias') {
      let set = rt.keyAliases.get(body.existingKeyId);
      if (!set) {
        set = new Set();
        rt.keyAliases.set(body.existingKeyId, set);
      }
      set.add(body.newKeyId);
    }

    // 分叉未解决：冻结一切部分数据字段发布（旧别名/传输重放都不能解锁）。
    if (rt.forked) {
      const fork = [...rt.openForks.values()].find((candidate) => candidate.resolvedBy === null);
      decisions.push(
        makeDecision(node, 'wait', 'wait_fork_unresolved', [
          `recovery covering forkParent=${fork?.parentHash ?? 'unknown'}`,
        ]),
      );
      return 'settled';
    }

    switch (body.type) {
      case 'update':
      case 'rehost':
      case 'addAlias':
        break;
      case 'delete':
        break;
      case 'revoke':
      case 'addKeyAlias':
        break;
      default:
        decisions.push(makeDecision(node, 'reject', 'reject_malformed'));
        return 'settled';
    }

    decisions.push(makeDecision(node, 'publish', 'ok'));
    return 'settled';
  }

  #markWaiting(rt: ObjectRuntime, node: EventNode, decisions: InternalDecision[]): void {
    const body = node.body;
    if (body.type !== 'recovery') {
      const auth = checkAuthorization(body, node.auth, this.#resolver);
      if (!auth.ok && auth.reason === 'wait_unknown_key') {
        decisions.push(makeDecision(node, 'wait', 'wait_unknown_key', [...auth.unknownKeyIds]));
        return;
      }
      if (!auth.ok) {
        decisions.push(makeDecision(node, 'reject', auth.reason));
        return;
      }
    }
    if (rt.forked) {
      const fork = [...rt.openForks.values()].find((candidate) => candidate.resolvedBy === null);
      decisions.push(
        makeDecision(node, 'wait', 'wait_fork_unresolved', [
          `recovery covering forkParent=${fork?.parentHash ?? 'unknown'}`,
        ]),
      );
      return;
    }
    decisions.push(
      makeDecision(node, 'wait', 'wait_missing_ancestor', [`event prev=${body.prev ?? 'null'}`]),
    );
  }

  #handleRecovery(rt: ObjectRuntime, node: EventNode, decisions: InternalDecision[]): 'settled' | 'waiting' {
    const body = node.body as RecoveryEvent;
    const fork = rt.openForks.get(body.forkParent);
    if (!fork) {
      // 分叉证据可能乱序后到：保持等待，由最终 markWaiting 统一给出所需证据。
      return 'waiting';
    }
    if (fork.resolvedBy) {
      decisions.push(makeDecision(node, 'reject', 'reject_not_a_fork'));
      return 'settled';
    }

    const [tipA, tipB] = body.branchTips;
    const twoDistinct = tipA !== tipB && body.branchTips.length === 2;
    const coversBoth = twoDistinct && fork.branches.has(tipA) && fork.branches.has(tipB);
    if (!coversBoth) {
      const missing: string[] = [];
      if (!fork.branches.has(tipA)) missing.push(`branch tip ${tipA}`);
      if (tipA !== tipB && !fork.branches.has(tipB)) missing.push(`branch tip ${tipB}`);
      decisions.push(
        makeDecision(node, 'wait', 'wait_recovery_threshold', [
          'both distinct branch tips deriving from the same forkParent',
          ...missing,
        ]),
      );
      return 'settled';
    }
    if (!body.branchTips.includes(body.canonicalTip)) {
      decisions.push(makeDecision(node, 'reject', 'reject_canonical_tip'));
      return 'settled';
    }

    const policy = rt.policy;
    if (!policy) {
      decisions.push(makeDecision(node, 'reject', 'reject_recovery_membership'));
      return 'settled';
    }

    const committed = new Set(policy.members);
    const voters = new Set<string>();
    const unknownKeys: string[] = [];
    for (const vote of body.votes) {
      const memberEquivalent = [...committed].some((member) =>
        keyEquivalent(rt, vote.memberKeyId, member),
      );
      if (!memberEquivalent) {
        decisions.push(makeDecision(node, 'reject', 'reject_recovery_membership'));
        return 'settled';
      }
      if (rt.revokedAll.has(vote.memberKeyId)) {
        decisions.push(makeDecision(node, 'reject', 'reject_fully_revoked_member'));
        return 'settled';
      }
      if (voters.has(vote.memberKeyId)) continue;
      const publicKey = this.#resolver.resolveKey(vote.memberKeyId);
      if (publicKey === null) {
        unknownKeys.push(vote.memberKeyId);
        continue;
      }
      const claim = (() => {
        const { votes: _votes, ...rest } = body;
        void _votes;
        return rest;
      })();
      if (!verifyCanonical(publicKey, claim, vote.signature)) {
        decisions.push(makeDecision(node, 'reject', 'reject_bad_signature'));
        return 'settled';
      }
      voters.add(vote.memberKeyId);
    }

    if (unknownKeys.length > 0) {
      decisions.push(makeDecision(node, 'wait', 'wait_unknown_key', unknownKeys));
      return 'settled';
    }
    if (voters.size < policy.threshold) {
      decisions.push(
        makeDecision(node, 'wait', 'wait_recovery_threshold', [
          `${policy.threshold - voters.size} more committed member vote(s)`,
        ]),
      );
      return 'settled';
    }

    this.#mergeFork(rt, fork, body, node);
    decisions.push(makeDecision(node, 'publish', 'ok'));
    return 'settled';
  }

  #mergeFork(rt: ObjectRuntime, fork: Fork, body: RecoveryEvent, node: EventNode): void {
    const canonicalState = rt.transfers.get(body.canonicalTip)!;

    fork.resolvedBy = node.hash;
    fork.canonicalTip = body.canonicalTip;
    rt.forked = [...rt.openForks.values()].some((candidate) => candidate.resolvedBy === null);

    const chain: string[] = [];
    let cursor: string | null = body.canonicalTip;
    while (cursor) {
      chain.unshift(cursor);
      cursor = rt.graph.nodes.get(cursor)?.body.prev ?? null;
    }
    rt.authority = chain;
    rt.currentTip = body.canonicalTip;
    rt.currentGen = canonicalState.gen;
    rt.holder = canonicalState.holder;
  }

  /**
   * 最终投影：仅沿权威链重放，保证败选支的字段/URL/别名/删除不会被发布。
   * 撤回集合由转让状态沿链继承，并在合并时取两支并集，因此恢复不会复活编辑权。
   */
  #project(rt: ObjectRuntime, decisions: InternalDecision[]): void {
    const decisionByHash = new Map(decisions.map((d) => [d.eventHash, d]));
    if (rt.forked) {
      for (const d of decisions) {
        const node = rt.graph.nodes.get(d.eventHash);
        if (!node) continue;
        if (d.kind === 'publish' && node.body.type !== 'create' && node.body.type !== 'recovery') {
          d.kind = 'wait';
          d.reason = 'wait_fork_unresolved';
          d.awaiting = ['fork resolution before any partial-field publication'];
        }
      }
      return;
    }

    const canonical = new Set(rt.authority);
    const mutations = [...rt.graph.nodes.values()]
      .filter((node) => node.body.prev && canonical.has(node.body.prev))
      .filter((node) => !['create', 'transfer', 'recovery'].includes(node.body.type))
      .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0));

    for (const node of mutations) {
      const d = decisionByHash.get(node.hash);
      if (d && d.kind === 'reject') continue;
      const signer = node.auth[0]?.keyId;
      if (
        node.body.type !== 'revoke' &&
        signer !== undefined &&
        (rt.revokedEdit.has(signer) ||
          rt.revokedAll.has(signer) ||
          [...rt.revokedEdit].some((revoked) => keyEquivalent(rt, signer, revoked)))
      ) {
        if (d) {
          d.kind = 'reject';
          d.reason = 'reject_revoked_editor';
          d.awaiting = [];
        }
        continue;
      }
      if (d && d.kind === 'wait') {
        d.kind = 'publish';
        d.reason = 'ok';
        d.awaiting = [];
      }
    }

    // 败选支上的转让/变更明确拒绝发布（事件仍保留在完整参考中）。
    for (const node of rt.graph.nodes.values()) {
      if (['create', 'recovery'].includes(node.body.type)) continue;
      const inAuthority =
        node.body.type === 'transfer'
          ? canonical.has(node.hash)
          : node.body.prev !== null && canonical.has(node.body.prev);
      const d = decisionByHash.get(node.hash);
      if (!inAuthority && d && d.kind === 'wait' && d.reason === 'wait_fork_unresolved') {
        d.kind = 'reject';
        d.reason = 'reject_stale_gen';
        d.awaiting = [];
      }
    }
  }

  #applyDescendantIsolation(
    runtimes: Map<string, ObjectRuntime>,
    isolated: Set<string>,
    decisions: InternalDecision[],
  ): void {
    for (const key of isolated) {
      const rt = runtimes.get(key);
      if (!rt) continue;
      const isForkRoot = rt.forked;
      if (isForkRoot) continue;
      for (const d of decisions) {
        if (rt.graph.nodes.has(d.eventHash) && d.kind === 'publish') {
          d.kind = 'wait';
          d.reason = 'wait_descendant_isolated';
          d.awaiting = ['ancestor fork recovery'];
        }
      }
    }
  }

  #buildViews(
    inbox: InboxGraph,
    runtimes: Map<string, ObjectRuntime>,
    isolated: Set<string>,
  ): Map<string, ObjectView> {
    const views = new Map<string, ObjectView>();
    for (const [key, rt] of runtimes) {
      if (!rt.rootHash && !rt.checkpoint) continue;
      const objectId = rt.rootHash
        ? rt.graph.nodes.get(rt.rootHash)!.body.id
        : rt.checkpoint!.object;
      if (isolated.has(key) && !rt.forked) {
        views.set(key, {
          id: objectId,
          holderKeyId: null,
          displayUrl: null,
          aliases: [],
          fields: {},
          gen: rt.currentGen,
          forked: false,
          deleted: this.#replayState(rt).deleted,
          authorityChain: [],
        });
        continue;
      }
      const projected = this.#replayState(rt);
      views.set(key, {
        id: objectId,
        holderKeyId: rt.forked ? null : rt.holder,
        displayUrl: rt.forked ? null : projected.displayUrl,
        aliases: rt.forked ? [] : projected.aliases,
        fields: rt.forked ? {} : projected.fields,
        gen: rt.currentGen,
        forked: rt.forked,
        deleted: rt.forked ? false : projected.deleted,
        authorityChain: rt.forked ? [] : [...rt.authority],
      });
    }
    return views;
  }

  #replayState(rt: ObjectRuntime): {
    fields: Record<string, unknown>;
    displayUrl: string | null;
    aliases: string[];
    deleted: boolean;
  } {
    const canonical = new Set(rt.authority);
    const fields: Record<string, unknown> = {};
    let displayUrl: string | null = rt.checkpoint?.displayUrl ?? null;
    const aliasSet = new Set<string>(rt.checkpoint?.aliases ?? []);
    let deleted = rt.checkpoint?.deleted ?? false;
    // 沿权威链按拓扑（因果）顺序重放，避免按哈希排序打乱代次。
    const ordered: EventNode[] = [];
    for (const anchorHash of rt.authority) {
      const anchor = rt.graph.nodes.get(anchorHash);
      if (anchor) ordered.push(anchor);
      const attached = [...rt.graph.nodes.values()]
        .filter((node) => node.body.prev === anchorHash && node.body.type !== 'transfer')
        .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0));
      ordered.push(...attached);
    }
    for (const node of ordered) {
      const body = node.body;
      if (body.type === 'create') {
        Object.assign(fields, body.fields ?? {});
        displayUrl = body.displayUrl;
        for (const alias of body.aliases ?? []) aliasSet.add(alias);
      } else if (body.type === 'update') {
        Object.assign(fields, body.fields);
      } else if (body.type === 'rehost') {
        displayUrl = body.displayUrl;
      } else if (body.type === 'transfer' && body.displayUrl !== null) {
        displayUrl = body.displayUrl;
      } else if (body.type === 'addAlias') {
        aliasSet.add(body.alias);
      } else if (body.type === 'delete') {
        deleted = true;
      }
    }
    return { fields, displayUrl, aliases: [...aliasSet].sort(), deleted };
  }
}
