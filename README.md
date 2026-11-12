# External Secrets命名空间与目标隔离

External Secrets Operator 按刷新间隔从外部密钥库同步成集群 Secret，攻击者可创建引用其他命名空间存储的 ExternalSecret 读取不属于自己的密钥，或让刷新写入已被他人拥有的 Secret。本包为 TypeScript 控制器提供供**每次调谐调用**的纯函数隔离判定，零依赖、无副作用。

## 判定规则

1. **存储引用与命名空间隔离**（`src/storeAccess.ts`）
   - `SecretStore` 为命名空间范围：只能被同命名空间的 ExternalSecret 引用，跨命名空间引用返回 `CROSS_NAMESPACE_STORE_REF`。
   - `ClusterSecretStore` 为集群范围：调用命名空间必须同时命中 `allowedNamespaces` 白名单与 `namespaceSelector`（`matchLabels` / `matchExpressions`），否则返回 `NAMESPACE_NOT_WHITELISTED` / `NAMESPACE_LABELS_REJECTED`。
   - 存储不存在或引用种类/名称不匹配返回 `STORE_NOT_FOUND`、`STORE_REF_KIND_UNSUPPORTED`。
2. **键空间隔离**（`src/keys.ts`）
   - 显式 `key` 必须命中存储的 `allowedKeyPrefixes`，否则 `KEY_PREFIX_VIOLATION`；无前缀配置返回 `NO_ALLOWED_KEY_PREFIX`。
   - `find.name` 为未锚定子串匹配：先用越前缀探针做静态保守判定，凡能命中非允许前缀字符串的正则返回 `FIND_REGEX_PREFIX_ESCAPE`（须以 `^team-a/` 这类前缀锚定）；提供 `providerKeys` 时再做权威运行时判定，实际匹配到越前缀键返回 `FIND_MATCHED_DISALLOWED_KEY`；正则无法编译返回 `FIND_REGEX_INVALID`。
3. **目标属主隔离**（`src/ownership.ts`）
   - `Owner` 策略下目标 Secret 已存在且属主不是本 ExternalSecret（uid 比对）返回 `FOREIGN_OWNER`；无属主或自身属主放行。
4. **Merge 保护标签**（`src/ownership.ts`）
   - 受保护键通过标签 `eso.example.com/protected-keys: "a,b"` 或 `eso.example.com/protected-key/<键名>: "true"` 声明；Merge 拟写入键与其有交集返回 `PROTECTED_KEY_OVERWRITE`。
5. **刷新安全**（`src/refresh.ts`）
   - 刷新失败（`refresh.failed`）：返回 `skip` 与 `REFRESH_FAILED`，`plannedData` 为空，绝不用空数据替换旧值。
   - 刷新成功但某键取到空字符串、而现有数据非空：剔除该键并给 `EMPTY_VALUE_DOWNGRADE` 提示；目标不存在时空值仍允许写入。
   - `creationPolicy: None` 且目标缺失：返回 `skip` 与 `TARGET_MISSING_WITH_NONE_POLICY`，不代为创建。

## 决策输出

`evaluateIsolation` 返回 `IsolationDecision`：

- `action: 'deny'`：出现 `DENIAL_CODES` 中的拒绝性违例，控制器必须停止写入并上报事件。
- `action: 'skip'`：无安全违例但本次不写入（刷新失败、None 策略目标缺失）。
- `action: 'apply'`：全部通过，`plannedData` 为已做空值降级的实际写入数据。

## 用法

```ts
import { evaluateIsolation } from './index.ts';

const decision = evaluateIsolation({
  externalSecret: {
    namespace: 'team-a',
    storeRefKind: 'SecretStore',
    storeRefName: 'team-store',
    dataFrom: [{ find: { name: '^team-a/' } }],
    targetSecretName: 'app-secret',
    creationPolicy: 'Owner',
  },
  store: {
    kind: 'SecretStore',
    name: 'team-store',
    scope: 'namespace',
    namespace: 'team-a',
    allowedKeyPrefixes: ['team-a/'],
  },
  namespaceLabels: { 'secrets/tenant': 'team-a' },
  existingTarget: undefined,
  self: {
    apiVersion: 'external-secrets.io/v1',
    kind: 'ExternalSecret',
    name: 'app-es',
    uid: 'uid-self-0001',
  },
  providerKeys: ['team-a/db/password'],
  refresh: { failed: false, fetched: { 'team-a/db/password': 's3cr3t' } },
});

if (decision.action === 'deny') {
  // 记录 decision.denials，不要写 Secret
} else if (decision.action === 'apply') {
  // 按 decision.plannedData 写入目标 Secret
}
```

## 测试

`npm test`（Node 内置 test runner + `--experimental-strip-types`，无需安装依赖）。测试覆盖跨命名空间引用、正则越前缀、他人属主、Merge 保护覆盖与失败刷新五类场景。
