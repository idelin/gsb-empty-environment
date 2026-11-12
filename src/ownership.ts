import type {
  EsoIdentity,
  ExistingTargetSecret,
  Finding,
} from './types.ts';

const PROTECTED_LIST_LABEL = 'eso.example.com/protected-keys';
const PROTECTED_KEY_LABEL_PREFIX = 'eso.example.com/protected-key/';

/** 从目标 Secret 标签解析受保护键集合。 */
export function parseProtectedKeys(
  labels: Record<string, string> | undefined,
): Set<string> {
  const protectedKeys = new Set<string>();
  if (!labels) {
    return protectedKeys;
  }

  const listed = labels[PROTECTED_LIST_LABEL];
  if (listed) {
    for (const key of listed.split(',')) {
      const trimmed = key.trim();
      if (trimmed) {
        protectedKeys.add(trimmed);
      }
    }
  }

  for (const [labelKey, value] of Object.entries(labels)) {
    if (
      labelKey.startsWith(PROTECTED_KEY_LABEL_PREFIX) &&
      (value === 'true' || value === '')
    ) {
      protectedKeys.add(labelKey.slice(PROTECTED_KEY_LABEL_PREFIX.length));
    }
  }

  return protectedKeys;
}
function isSameController(
  owner: { apiVersion: string; kind: string; name: string; uid: string },
  self: EsoIdentity,
): boolean {
  return (
    owner.apiVersion === self.apiVersion &&
    owner.kind === self.kind &&
    owner.name === self.name &&
    owner.uid === self.uid
  );
}

/**
 * Owner 策略：目标 Secret 已存在且属主不是本 ExternalSecret 时拒绝，
 * 防止把刷新结果写进他人拥有的 Secret。无属主或自身属主放行。
 */
export function evaluateTargetOwnership(
  existing: ExistingTargetSecret | undefined,
  self: EsoIdentity,
): Finding[] {
  if (!existing) {
    return [];
  }
  const foreign = existing.ownerReferences.find(
    (owner) => !isSameController(owner, self),
  );
  if (foreign) {
    return [
      {
        code: 'FOREIGN_OWNER',
        message: `目标 Secret ${existing.namespace}/${existing.name} 已被 ${foreign.kind}/${foreign.name} 拥有，拒绝接管`,
        details: {
          ownerKind: foreign.kind,
          ownerName: foreign.name,
          ownerUid: foreign.uid,
        },
      },
    ];
  }
  return [];
}

/**
 * Merge 策略：拟写入键与受保护键有交集即拒绝，
 * 避免覆盖带保护标签的现有键。
 */
export function evaluateProtectedOverwrite(
  existing: ExistingTargetSecret | undefined,
  incomingKeys: Iterable<string>,
): Finding[] {
  if (!existing) {
    return [];
  }
  const protectedKeys = parseProtectedKeys(existing.labels);
  if (protectedKeys.size === 0) {
    return [];
  }
  const conflicts = [...incomingKeys].filter((key) => protectedKeys.has(key));
  if (conflicts.length === 0) {
    return [];
  }
  return [
    {
      code: 'PROTECTED_KEY_OVERWRITE',
      message: `Merge 拒绝覆盖目标 Secret ${existing.namespace}/${existing.name} 中带保护标签的键`,
      details: { protectedKeys: conflicts },
    },
  ];
}
