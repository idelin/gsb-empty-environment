Kubernetes 声明式准入体系（`ValidatingAdmissionPolicy` + `ValidatingAdmissionPolicyBinding`）把策略逻辑与作用域/参数解耦，多条 Binding 绑定到同一 Policy 时，资源匹配、参数解析、条件短路与动作合并隐藏着四项极易写错的规范细节：1）**资源规则与子资源匹配（`matchConstraints`）**：按 `(apiGroup, apiVersion, resource, subresource, operation)` 匹配，支持 `*` 通配与 `excludeResourceRules` 显式排除优先于 `resourceRules` 包含；特别注意子资源语义——`pods` **不**匹配 `pods/status`，`pods/*` 匹配 `pods/status` 但**不**匹配主资源 `pods`，只有 `*` 或显式同时列出才匹配两者！同时校验 `namespaceSelector` 与 `objectSelector`（支持 `matchLabels` 与 `In`/`NotIn`/`Exists`/`DoesNotExist` 表达式）；2）**参数绑定与缺参处置（`paramRef`）**：支持按固定名称或标签选择器从指定命名空间解析多份参数对象（一条 Binding 匹配到 `K` 个参数对象时，策略须对每个参数对象分别独立求值！），当参数对象未找到时严格按 `parameterNotFoundAction` 执行 `Allow`（跳过该 Binding）或 `Deny`（直接失败！）；3）**`matchConditions` 短路与 `failurePolicy` 异常分支**：先依次求值全部 `matchConditions`，只要任一条件求值为 `false` 即判定不适用并跳过；若某个 `matchCondition` 或 `validation` 表达式抛出运行时错误（如除零、缺字段空引用），按策略的 `failurePolicy` 决定忽略放行（`Ignore`）还是按失败动作处理（`Fail`）；4）**多 Binding 与多 Validation 动作全量收集**：每条 `validation` 独立求值（不因首条失败就跳过后续 `Warn`/`Audit` 条目！），将所有命中 Binding 的失败按各自 `validationActions`（`Deny`/`Warn`/`Audit` 任意组合）汇总——只要任一失败绑定含 `Deny` 则整体请求被拒绝，且无论最终放行还是拒绝都必须准确输出全部 `Warn` 提示列表与 `Audit` 审计键值！为 API 服务器准入层交付可导入的 TypeScript `ValidatingAdmissionPolicy` 评估引擎。验收覆盖 `pods` vs `pods/*` 子资源与 `excludeResourceRules` 优先、标签选择器多参数展开与 `parameterNotFoundAction`、`matchConditions` 运行时错误在 `Fail`/`Ignore` 下分叉、以及多 Binding 并发触发 `Deny`+`Warn`+`Audit` 完整汇总。

## 引擎实现

零运行时依赖，源码位于 `src/`，内置一个 CEL 子集求值器（`src/cel/`，含词法/语法/求值三层、int64 装箱整数、短路逻辑与 `all/exists/exists_one/filter/map/has/size` 宏）。

- `src/matching.ts`：`matchConstraints` 资源规则、子资源、排除优先与标签选择器。
- `src/parameters.ts`：`ParameterStore` 与 `paramRef` 固定名/标签选择器解析。
- `src/engine.ts`：`ValidatingAdmissionEngine`，编排匹配、参数展开、`matchConditions` 短路、`failurePolicy` 分叉与 `Deny/Warn/Audit` 汇总。
- `src/types.ts`：对齐 `admissionregistration.k8s.io/v1` 的类型声明。

## 快速开始

```ts
import { ValidatingAdmissionEngine } from './dist/src/index.js';

const engine = new ValidatingAdmissionEngine();

engine.registerPolicy({
  metadata: { name: 'replica-limit' },
  spec: {
    paramKind: { apiVersion: 'policy.example.com/v1', kind: 'ReplicaLimit' },
    matchConstraints: {
      resourceRules: [
        { apiGroups: ['apps'], apiVersions: ['v1'], resources: ['deployments'], operations: ['CREATE', 'UPDATE'] },
      ],
    },
    validations: [
      { expression: 'object.spec.replicas <= params.maxReplicas', message: 'replicas exceed the limit' },
    ],
  },
});

engine.addParameter({
  apiVersion: 'policy.example.com/v1',
  kind: 'ReplicaLimit',
  metadata: { namespace: 'team-a', name: 'default', labels: { tier: 'gold' } },
  maxReplicas: 3,
});

engine.registerBinding({
  metadata: { name: 'team-a-binding' },
  spec: {
    policyName: 'replica-limit',
    paramRef: { namespace: 'team-a', selector: { matchLabels: { tier: 'gold' } } },
    validationActions: ['Deny', 'Warn', 'Audit'],
  },
});

const decision = engine.evaluate({
  operation: 'CREATE',
  resource: { apiGroup: 'apps', apiVersion: 'v1', resource: 'deployments' },
  namespace: 'team-a',
  object: { spec: { replicas: 5 } },
});

decision.denied;            // true
decision.warnings;          // 全部 Warn 提示（即使被 Deny 也保留）
decision.auditAnnotations;  // 全部 Audit 键值（即使被 Deny 也保留）
decision.bindingEvaluations; // 每条 Binding 的匹配、参数键、失败与错误明细
```

## 关键语义

- **子资源**：`pods` 仅匹配主资源；`pods/*` 仅匹配子资源（如 `pods/status`），不匹配主资源；`*` 或同时显式列出两者才能覆盖。
- **排除优先**：任一 `excludeResourceRules` 命中即否决，即使 `resourceRules` 命中。
- **多参数展开**：标签选择器解析出 `K` 个参数对象时，策略对每份参数独立求值，失败结果携带对应参数命名空间/名称。
- **缺参动作**：`parameterNotFoundAction: Allow` 跳过该 Binding；`Deny`（默认）直接产出失败。
- **短路与异常**：`matchConditions` 顺序求值，首个 `false` 短路；表达式运行时错误按 `failurePolicy` 在 `Ignore`（放行/跳过）与 `Fail`（按 Binding 的 `validationActions` 失败）间分叉。
- **全量收集**：每条 `validation` 都会执行；所有命中 Binding 的失败按各自动作汇总，任一 `Deny` 即拒绝，`Warn` 列表与 `Audit` 注解在放行与拒绝路径下都完整输出。

## 构建与测试

```bash
npm run build     # tsc 编译到 dist/，并规范化声明文件的 .js 扩展
npm run typecheck # 仅类型检查
npm test          # 编译后运行 node:test（tests/ 下五个套件）
```
