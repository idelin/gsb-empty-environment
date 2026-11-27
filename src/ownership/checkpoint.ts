import { canonicalJson } from './canonical.ts';
import { signBytes, verifyBytes } from './crypto.ts';
import { resolveOwnership } from './resolver.ts';
import type {
  Checkpoint,
  Envelope,
  GenDigest,
  KeyResolver,
  ObjectView,
} from './types.ts';
import { objectIdKey } from './types.ts';

export interface FoldOptions {
  /** 受信检查点签发者公钥（base64url 原始 Ed25519）。 */
  readonly checkpointKeyB64: string;
  readonly prior?: Checkpoint;
}

/**
 * 把已确认前缀压缩为检查点。关键不变量：
 * - 保留全部同代异摘要 genDigests，压缩后仍能识别分叉/同代竞争；
 * - 保留全部旧别名 aliases；
 * - 保留 revokedEdit/revokedAll，恢复不会复活已撤回编辑权。
 */
export function foldCheckpoint(
  envelopes: readonly Envelope[],
  resolver: KeyResolver,
  objectView: ObjectView,
  options: FoldOptions,
): Checkpoint {
  const resolution = resolveOwnership(envelopes, resolver, { checkpoints: options.prior ? [options.prior] : [] });
  const key = objectIdKey(objectView.id);
  const resolvedView = resolution.views.get(key);
  if (!resolvedView || resolvedView.forked) {
    throw new Error(`no resolvable object for ${key}`);
  }

  const genDigests: GenDigest[] = (() => {
    // 通过完整事件重算同代异摘要；检查点不丢弃这些摘要。
    const merged = new Map<number, Set<string>>();
    for (const priorDigest of options.prior?.genDigests ?? []) {
      merged.set(priorDigest.gen, new Set(priorDigest.digests));
    }
    for (const envelope of envelopes) {
      if (objectIdKey(envelope.body.id) !== key) continue;
      const set = merged.get(envelope.body.gen) ?? new Set<string>();
      set.add(envelope.eventHash);
      merged.set(envelope.body.gen, set);
    }
    return [...merged.entries()]
      .filter(([, digests]) => digests.size > 1)
      .map(([gen, digests]) => ({ gen, digests: [...digests].sort() }))
      .sort((a, b) => a.gen - b.gen);
  })();

  const revokedAll = new Set<string>(options.prior?.revokedAll ?? []);
  const revokedEdit = new Set<string>(options.prior?.revokedEdit ?? []);
  for (const envelope of envelopes) {
    if (objectIdKey(envelope.body.id) !== key) continue;
    if (envelope.body.type === 'revoke') {
      if (envelope.body.scope === 'all') {
        revokedAll.add(envelope.body.targetKeyId);
        revokedEdit.add(envelope.body.targetKeyId);
      } else {
        revokedEdit.add(envelope.body.targetKeyId);
      }
    }
  }

  const policy =
    envelopes.find((envelope) => envelope.body.type === 'create' && objectIdKey(envelope.body.id) === key)
      ?.body ?? null;
  const recoveryPolicy =
    policy && policy.type === 'create'
      ? policy.recoveryPolicy
      : options.prior?.recoveryPolicy ?? { threshold: 0, members: [] };

  const tipHash = objectView.authorityChain[objectView.authorityChain.length - 1] ?? options.prior?.tipHash ?? '';
  const unsigned: Omit<Checkpoint, 'checkpointSignature'> = {
    object: objectView.id,
    tipHash,
    gen: objectView.gen,
    holderKeyId: objectView.holderKeyId ?? options.prior?.holderKeyId ?? '',
    displayUrl: objectView.displayUrl ?? options.prior?.displayUrl ?? '',
    aliases: [...objectView.aliases].sort(),
    genDigests,
    revokedEdit: [...revokedEdit].sort(),
    revokedAll: [...revokedAll].sort(),
    recoveryPolicy,
    deleted: objectView.deleted,
  };
  // 注意：options.checkpointKeyB64 是公钥，仅用于验签；签名由受信私钥产生。
  // 这里返回未签名对象，调用方应使用 signWithCheckpointKey 注入受信签名。
  return unsigned as Checkpoint;
}

export function checkpointPayload(checkpoint: Omit<Checkpoint, 'checkpointSignature'>): Buffer {
  return Buffer.from(canonicalJson(checkpoint), 'utf8');
}

/** 用受信检查点公钥验证检查点签名（签名由持有对应私钥的一方产生，见 signWithKey）。 */
export function verifyCheckpoint(checkpoint: Checkpoint, checkpointKeyB64: string): boolean {
  if (!checkpoint.checkpointSignature) return false;
  const { checkpointSignature, ...unsigned } = checkpoint;
  return verifyBytes(checkpointKeyB64, checkpointPayload(unsigned), checkpointSignature);
}

/** 由检查点签发私钥对检查点签名（服务端受信进程使用）。 */
export function signWithCheckpointKey(
  checkpoint: Omit<Checkpoint, 'checkpointSignature'>,
  privateKeyPem: string,
): Checkpoint {
  const signature = signBytes(privateKeyPem, checkpointPayload(checkpoint));
  return { ...checkpoint, checkpointSignature: signature };
}
