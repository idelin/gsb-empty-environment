import { evaluateIsolation } from '../src/isolate.ts';
import type {
  EsoIdentity,
  ExternalSecretInput,
  IsolationDecision,
  IsolationEvaluation,
  RefreshResult,
  StoreDefinition,
} from '../src/types.ts';

export const SELF: EsoIdentity = {
  apiVersion: 'external-secrets.io/v1',
  kind: 'ExternalSecret',
  name: 'app-es',
  uid: 'uid-self-0001',
};

export function namespaceStore(
  overrides: Partial<StoreDefinition> = {},
): StoreDefinition {
  return {
    kind: 'SecretStore',
    name: 'team-store',
    scope: 'namespace',
    namespace: 'team-a',
    allowedKeyPrefixes: ['team-a/'],
    ...overrides,
  };
}

export function clusterStore(
  overrides: Partial<StoreDefinition> = {},
): StoreDefinition {
  return {
    kind: 'ClusterSecretStore',
    name: 'platform-store',
    scope: 'cluster',
    allowedNamespaces: ['team-a'],
    namespaceSelector: {
      matchLabels: { 'secrets/tenant': 'team-a' },
    },
    allowedKeyPrefixes: ['team-a/'],
    ...overrides,
  };
}

export function externalSecret(
  overrides: Partial<ExternalSecretInput> = {},
): ExternalSecretInput {
  return {
    namespace: 'team-a',
    storeRefKind: 'SecretStore',
    storeRefName: 'team-store',
    dataFrom: [{ key: 'team-a/db/password' }],
    targetSecretName: 'app-secret',
    creationPolicy: 'Owner',
    ...overrides,
  };
}

export function refresh(
  overrides: Partial<RefreshResult> = {},
): RefreshResult {
  return {
    failed: false,
    fetched: { 'team-a/db/password': 's3cr3t' },
    ...overrides,
  };
}

export function evaluate(
  overrides: Partial<IsolationEvaluation> = {},
): IsolationDecision {
  const es = overrides.externalSecret ?? externalSecret();
  const store =
    !('store' in overrides)
      ? es.storeRefKind === 'ClusterSecretStore'
        ? clusterStore()
        : namespaceStore()
      : overrides.store;
  return evaluateIsolation({
    externalSecret: es,
    store,
    namespaceLabels: overrides.namespaceLabels ?? {
      'secrets/tenant': 'team-a',
    },
    existingTarget: overrides.existingTarget,
    self: overrides.self ?? SELF,
    providerKeys: overrides.providerKeys,
    refresh: overrides.refresh ?? refresh(),
  });
}
