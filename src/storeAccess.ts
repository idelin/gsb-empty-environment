import { matchesLabelSelector } from './selector.ts';
import type {
  ExternalSecretInput,
  Finding,
  StoreDefinition,
} from './types.ts';

/**
 * 存储引用与命名空间隔离：
 * - SecretStore 为命名空间范围，只能被同命名空间的 ExternalSecret 引用；
 * - ClusterSecretStore 为集群范围，调用方命名空间必须命中白名单且满足标签选择器。
 */
export function evaluateStoreAccess(
  externalSecret: ExternalSecretInput,
  store: StoreDefinition | undefined,
  namespaceLabels: Record<string, string> = {},
): Finding[] {
  const findings: Finding[] = [];
  const { namespace, storeRefKind, storeRefName } = externalSecret;

  if (storeRefKind !== 'SecretStore' && storeRefKind !== 'ClusterSecretStore') {
    findings.push({
      code: 'STORE_REF_KIND_UNSUPPORTED',
      message: `不支持的存储引用种类 ${storeRefKind}`,
      details: { storeRefKind },
    });
    return findings;
  }

  if (!store) {
    findings.push({
      code: 'STORE_NOT_FOUND',
      message: `存储 ${storeRefKind}/${storeRefName} 不存在或不可见`,
      details: { storeRefKind, storeRefName },
    });
    return findings;
  }

  if (store.kind !== storeRefKind || store.name !== storeRefName) {
    findings.push({
      code: 'STORE_NOT_FOUND',
      message: `存储引用 ${storeRefKind}/${storeRefName} 与解析到的存储不匹配`,
      details: { storeRefKind, storeRefName, resolved: `${store.kind}/${store.name}` },
    });
    return findings;
  }

  if (store.scope === 'namespace') {
    if (!store.namespace || store.namespace !== namespace) {
      findings.push({
        code: 'CROSS_NAMESPACE_STORE_REF',
        message: `命名空间范围存储 ${store.name} 位于 ${store.namespace ?? '?'}，拒绝被 ${namespace} 跨命名空间引用`,
        details: {
          callerNamespace: namespace,
          storeNamespace: store.namespace ?? '',
        },
      });
    }
    return findings;
  }

  const whitelist = store.allowedNamespaces ?? [];
  if (!whitelist.includes(namespace)) {
    findings.push({
      code: 'NAMESPACE_NOT_WHITELISTED',
      message: `命名空间 ${namespace} 不在集群存储 ${store.name} 的白名单内`,
      details: { namespace, allowedNamespaces: whitelist },
    });
  }

  if (!matchesLabelSelector(namespaceLabels, store.namespaceSelector)) {
    findings.push({
      code: 'NAMESPACE_LABELS_REJECTED',
      message: `命名空间 ${namespace} 的标签不满足集群存储 ${store.name} 的选择器条件`,
    });
  }

  return findings;
}
