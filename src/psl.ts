/**
 * 公共后缀列表（PSL）规则求值。
 *
 * 规则三类：
 * - 精确规则：`com`
 * - 通配规则：`*.ck`（规则标签数 = 通配域标签数 + 1）
 * - 例外规则：`!www.ck`（命中时可注册域为去掉 `!` 的部分）
 *
 * 本模块不触碰网络：规则存在性完全由调用方基于 Merkle 证明还原。
 */

export type PslRuleType = 'exact' | 'wildcard' | 'exception';

export interface PslResolution {
  readonly registrableDomain: string;
  readonly matchedRuleType: PslRuleType | 'default';
  readonly matchedRuleKey: string | null;
}

export function normalizeHost(host: string): string {
  let normalized = host.trim().toLowerCase();
  if (normalized.endsWith('.')) {
    normalized = normalized.slice(0, -1);
  }
  return normalized;
}

function classifyRule(rule: string): PslRuleType {
  if (rule.startsWith('!')) {
    return 'exception';
  }
  if (rule.startsWith('*.')) {
    return 'wildcard';
  }
  return 'exact';
}

/** 由原始规则文本构建规则映射（Merkle 叶子键即规则文本）。 */
export function buildRuleMap(rules: readonly string[]): Map<string, PslRuleType> {
  const map = new Map<string, PslRuleType>();
  for (const rule of rules) {
    const normalized = rule.trim().toLowerCase();
    if (normalized.length === 0 || normalized.startsWith('//')) {
      continue;
    }
    map.set(normalized, classifyRule(normalized));
  }
  return map;
}

/**
 * 生成主机需要核验的全部候选规则键：
 * 对每个标签后缀分别检查精确、通配与例外形式。
 */
export function candidateKeys(host: string): string[] {
  const normalized = normalizeHost(host);
  const labels = normalized.split('.').filter((label) => label.length > 0);
  const keys: string[] = [];
  for (let i = 0; i < labels.length; i += 1) {
    const suffix = labels.slice(i).join('.');
    keys.push(suffix, `*.${suffix}`, `!${suffix}`);
  }
  return keys;
}

function labelCount(value: string): number {
  return value.split('.').filter((label) => label.length > 0).length;
}

/**
 * 基于已验证存在的规则键集合求值 PSL。
 * presentKeys 必须先经过 Merkle 包含/缺失证明核验。
 */
export function resolvePsl(host: string, presentKeys: ReadonlySet<string>): PslResolution {
  const normalized = normalizeHost(host);
  const labels = normalized.split('.').filter((label) => label.length > 0);

  let bestKey: string | null = null;
  let bestType: PslRuleType | null = null;
  let bestLabels = -1;

  for (const key of presentKeys) {
    const type = classifyRule(key);
    const bare = type === 'exception' ? key.slice(1) : type === 'wildcard' ? key.slice(2) : key;
    const bareLabels = labelCount(bare);
    const matches =
      normalized === bare ||
      (type === 'wildcard'
        ? normalized.endsWith(`.${bare}`) && labelCount(normalized) > bareLabels
        : normalized.endsWith(`.${bare}`));
    if (!matches) {
      continue;
    }
    // 例外规则永远优先；其余按最长（标签最多）胜出。
    const wins =
      type === 'exception' && bestType !== 'exception'
        ? true
        : bestType === 'exception'
          ? false
          : bareLabels > bestLabels;
    if (wins) {
      bestKey = key;
      bestType = type;
      bestLabels = bareLabels;
    }
  }

  if (bestKey === null || bestType === null) {
    return {
      registrableDomain: labels.length <= 1 ? normalized : labels.slice(-2).join('.'),
      matchedRuleType: 'default',
      matchedRuleKey: null,
    };
  }

  if (bestType === 'exception') {
    return { registrableDomain: bestKey.slice(1), matchedRuleType: 'exception', matchedRuleKey: bestKey };
  }

  // 精确规则取“规则标签数 + 1”；通配规则的通配符本身占一个标签，同样 +1。
  const take = bestLabels + 1;
  return {
    registrableDomain: labels.slice(-take).join('.'),
    matchedRuleType: bestType,
    matchedRuleKey: bestKey,
  };
}
