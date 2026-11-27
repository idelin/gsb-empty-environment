# 零依赖 GraphQL 静态安全网关组件

面向多租户 API 网关的 TypeScript 服务端宿主，提供可直接导入的、**零运行时外部依赖**的
GraphQL 词法/语法解析器与执行前静态安全策略验证组件。所有校验在查询执行之前完成，
任一硬性策略不通过即返回完整违规清单并拒绝执行。

## 解决的攻击面

- **别名放大 / 批量撞库 DoS**：同一层数百个别名重复调用高开销字段，简单括号深度限制无法识别。
- **片段循环 / 指数展开炸弹**：具名片段与内联片段（`...on`）相互引用，触发解析栈溢出或展开爆炸。
- **条件指令越权**：用 `@include(if: $v)` / `@skip(if: $v)` 把受限字段藏进条件分支，绕过只看静态文本的扫描。
- **租户作用域参数覆盖**：客户端尝试在参数或嵌套输入对象中覆盖服务端注入的 `tenantId` 等敏感字段。
- **内省探测**：默认阻断 `__schema` / `__type`。

## 模块结构

| 文件 | 职责 |
| --- | --- |
| `src/lexer.ts` | 从零实现的词法分析器：名称、标点、整数/浮点、普通/块字符串、注释、BOM、行列号定位 |
| `src/parser.ts` | 递归下降语法分析器：操作、变量定义、选择集、别名、参数、指令、具名/内联片段，含可配置嵌套深度保护 |
| `src/types.ts` | AST、Schema 输入、角色策略、违规与复杂度上界的类型定义 |
| `src/typeRef.ts` | 类型引用字符串/AST 解析与 GraphQL 变量可赋值（子类型）规则 |
| `src/validator.ts` | 静态安全策略校验核心：循环检测、记忆化展开、授权、变量与敏感参数检查、限额判定 |
| `src/index.ts` | 统一导出与 `parseGraphQL` / `validateGraphQL` 便捷函数 |

## 快速使用

```ts
import { validateGraphQL, type SchemaInput, type SecurityPolicyInput } from './src/index.ts';

const schema: SchemaInput = {
  queryType: 'Query',
  types: {
    Query: {
      kind: 'OBJECT',
      fields: {
        users: {
          type: '[User!]!',
          weight: 5,
          args: {
            first: { type: 'Int' },
            tenantId: { type: 'ID!', sensitive: true },
          },
        },
      },
    },
    User: { kind: 'OBJECT', fields: { id: { type: 'ID!' } } },
  },
};

const policy: SecurityPolicyInput = {
  role: 'viewer',
  maxDepth: 8,
  maxAliasMultiplier: 100,
  maxComplexity: 5000,
  maxListSize: 50,
  policies: { viewer: { fields: { 'Query.users': true, 'User.id': true } } },
};

const result = validateGraphQL(schema, policy, queryText);
if (!result.ok) {
  // 执行前硬拒绝；result.violations 含规范路径、片段链与复杂度上界
}
```

## 策略语义

### 片段循环与展开

- 三色标记 DFS 检出任意直接或间接循环，违规携带完整循环链（如 `A -> B -> C -> A`），
  命中循环即硬拒绝，绝不进行指数级展开。
- 无环片段以 DAG 方式**记忆化**分析：每个片段只计算一次内部结构，在每个使用点按倍数累计，
  保证线性时间且统计结果等价于完全展开。
- 内联片段与具名片段不增加字段深度；深度按真实字段层级计算。

### 确定性复杂度上界

- `maxDepth`：完全展开后的最大字段深度。
- `aliasMultiplier`：完全展开后字段/解析器调用总数上界；列表返回字段按
  `first` / `limit` 乘法因子计入其所有子树（变量分页按 `maxListSize` 取最坏值，
  超大字面量收敛到 `maxListSize`），别名重复调用按次累计。
- `aliasedFieldCount`：使用别名的字段定义数，便于单独观测别名滥用。
- `complexityUpperBound`：节点 `weight`（缺省 1）乘以所在列表基数后的加权总和。

### 最坏情况授权

- 对**每个可达字段**执行授权，无论其藏于具名片段、内联片段、别名还是
  `@include` / `@skip` 条件分支——条件分支默认按“可能执行”处理。
- 授权按真实字段名（忽略别名）以 `TypeName.fieldName` 键查角色策略表；
  未命中时回退角色 `defaultAllow`，再回退全局 `defaultAllow`（默认白名单制拒绝）。
- 违规输出规范路径（如 `Query.users.orders.secret`）与片段展开链（如 `['A','B']`）。
- 默认阻断 `__schema` / `__type`；`__typename` 视为元字段始终放行。

### 变量与敏感参数

- 所有 `$var` 引用必须由当前操作声明，并按 GraphQL 可赋值规则校验结构与可空性；
  可空变量传入非空参数且无默认值报 `VARIABLE_REQUIRED`。
- `@include` / `@skip` 的 `if` 必须是布尔变量或布尔字面量。
- Schema 中标记 `sensitive: true` 的参数或输入对象字段，一旦客户端显式传值即报
  `SENSITIVE_ARG_OVERRIDE`，确保租户作用域只能由服务端注入。

### 违规码

`SYNTAX_ERROR`、`DUPLICATE_FRAGMENT`、`FRAGMENT_CYCLE`、`UNKNOWN_FRAGMENT`、
`UNKNOWN_TYPE`、`UNKNOWN_FIELD`、`FIELD_FORBIDDEN`、`INTROSPECTION_BLOCKED`、
`SENSITIVE_ARG_OVERRIDE`、`VARIABLE_NOT_DEFINED`、`VARIABLE_TYPE_MISMATCH`、
`VARIABLE_REQUIRED`、`INVALID_LIST_ARGUMENT`、`LIMIT_DEPTH_EXCEEDED`、
`LIMIT_ALIAS_EXCEEDED`、`LIMIT_COMPLEXITY_EXCEEDED`。

## 测试

```sh
npm test
```

测试基于 Node 内置 test runner（`node --experimental-strip-types --test`），无第三方依赖。
覆盖：词法/语法基线与嵌套保护、别名放大攻击、列表乘法因子、直接与间接循环片段、
DAG 片段复用、`@include`/`@skip` 条件越权、内省阻断、敏感租户参数覆盖、变量类型校验、
合法嵌套/变更/多操作查询。
