import type { DataFromEntry, Finding, StoreDefinition } from './types.ts';

/** 构造越前缀探针：对允许前缀做“邻近但不命中”的变体。 */
function buildSiblingProbes(prefixes: string[]): string[] {
  const probes: string[] = [];
  for (const prefix of prefixes) {
    if (prefix === '') {
      continue;
    }
    const last = prefix.charCodeAt(prefix.length - 1);
    const bumped = String.fromCharCode(last + 1);
    if (prefix.length > 1) {
      probes.push(prefix.slice(0, -1) + bumped);
    }
    const head = prefix[0];
    if (head !== undefined) {
      probes.push(`x${head}/${prefix}key`);
    }
  }
  return probes;
}

/**
 * 静态保守探针。find.name 是子串匹配（未锚定），
 * 任何能命中“非允许前缀开头”字符串的正则都视为可越界，
 * 迫使使用方以 ^allowed-prefix 锚定。
 */
export function buildPrefixEscapeProbes(prefixes: string[]): string[] {
  const base = [
    'secret',
    'password',
    'passwd',
    'credentials',
    'token',
    'admin/credential',
    'other-ns/key',
    '_private',
    'kube-system/token',
    'system/underlay/password',
    'X',
  ];
  const probes = [...base, ...buildSiblingProbes(prefixes)];
  return probes.filter((probe) => !prefixes.some((p) => probe.startsWith(p)));
}

function hasAllowedPrefix(key: string, prefixes: string[]): boolean {
  return prefixes.some((prefix) => key.startsWith(prefix));
}

function checkFindEntry(
  pattern: string,
  prefixes: string[],
  providerKeys: string[] | undefined,
): Finding[] {
  const findings: Finding[] = [];

  let regex: RegExp;
  try {
    regex = new RegExp(pattern);
  } catch {
    findings.push({
      code: 'FIND_REGEX_INVALID',
      message: `find.name 正则 ${pattern} 无法编译`,
      details: { pattern },
    });
    return findings;
  }

  for (const probe of buildPrefixEscapeProbes(prefixes)) {
    if (regex.test(probe)) {
      findings.push({
        code: 'FIND_REGEX_PREFIX_ESCAPE',
        message: `find.name 正则 ${pattern} 可匹配允许前缀之外的键（如 ${probe}），必须以前缀锚定`,
        details: { pattern, probe, allowedKeyPrefixes: prefixes },
      });
      return findings;
    }
  }

  if (providerKeys) {
    const disallowed = providerKeys.filter(
      (key) => regex.test(key) && !hasAllowedPrefix(key, prefixes),
    );
    if (disallowed.length > 0) {
      findings.push({
        code: 'FIND_MATCHED_DISALLOWED_KEY',
        message: `find.name 正则 ${pattern} 实际匹配到 ${disallowed.length} 个超出允许前缀的键`,
        details: { pattern, disallowedKeys: disallowed },
      });
    }
  }

  return findings;
}

/**
 * 键空间隔离：
 * - 显式 key 必须命中存储允许前缀；
 * - find.name 正则不得匹配允许前缀之外的键（静态探针 + 可选运行时判定）。
 */
export function evaluateKeyAccess(
  dataFrom: DataFromEntry[],
  store: StoreDefinition,
  providerKeys?: string[],
): Finding[] {
  const findings: Finding[] = [];
  const prefixes = store.allowedKeyPrefixes;

  if (prefixes.length === 0) {
    findings.push({
      code: 'NO_ALLOWED_KEY_PREFIX',
      message: `存储 ${store.name} 未配置任何允许的 key 前缀，拒绝读取`,
    });
    return findings;
  }

  for (const entry of dataFrom) {
    if (entry.key !== undefined) {
      if (!hasAllowedPrefix(entry.key, prefixes)) {
        findings.push({
          code: 'KEY_PREFIX_VIOLATION',
          message: `key ${entry.key} 不命中任何允许前缀`,
          details: { key: entry.key, allowedKeyPrefixes: prefixes },
        });
      }
    }
    if (entry.find?.name !== undefined) {
      findings.push(
        ...checkFindEntry(entry.find.name, prefixes, providerKeys),
      );
    }
  }

  return findings;
}
