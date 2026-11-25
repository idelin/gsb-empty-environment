# 覆盖验收包（Coverage Acceptance Package）

供 TypeScript 验签宿主导入的签名覆盖验收库与全子集枚举测试。场景是一个带公共
清单和若干可替换区域的分发包，供应商、构建方、发布方分别对不同属性签名。

## 安装与运行

无第三方依赖，要求 Node >= 22（使用内置 `node:test` 与 `--experimental-strip-types`
直接运行 TypeScript）：

```sh
node --experimental-strip-types --test "test/**/*.test.ts"
# 或
npm test
```

宿主导入入口为 `src/index.ts`（见 `package.json` 的 `exports`）。

## 领域模型（src/types.ts）

- `SignatureRecord`：单条签名，绑定载荷模式 `payloadSchema`、规范化版本
  `canonicalVersion`、包身份 `packageId`、半开字节区间 `coveredRange`、声明属性
  `declaredAttributes`，以及实际公钥材料 `keyMaterialId`、可选证书
  `certificateId` 和管理域 `domain`。
- `CanonicalSpec`：宿主拥有的规范化描述，支持折叠换行（CRLF -> LF）与去掉指定
  填充；`reverseMap` 是从规范坐标回到原字节区间的**有序**映射。
- `Manifest`：包身份、规范化表、区域、长度字段、必须属性、模式绑定
  `schemaBindings` 与管理域相斥规则 `exclusionRules`。
- `AttributeRequirement`：必须属性，列出其原始编码区间、影响解释的长度字段 id，
  以及按**实际公钥材料**计数的身份门槛。

## 验收规则到代码的映射

- **单签名完整背书**：一个属性只有在某一条签名的 `coveredRange` 同时完整覆盖其
  全部原始编码区间和每个影响解释的长度字段时才被背书。两份签名各覆盖一半不会被
  拼接成同一签名人的同意——候选过滤在 `Verifier.buildWitness` 中按单条记录做
  （`coversAll`），子集枚举只在已通过单签背书的记录之间进行。
  见 `test/01-baseline.test.ts` 的跨字段长度覆盖用例。
- **规范化与回映**：`src/canonicalize.ts` 生成规范字节与有序回映；折叠点保持
  “两个原字节 -> 一个规范字节”的独立条目，使折叠字符两侧可以分开签署
  （`test/02-canonical-folding.test.ts`）。回映歧义由
  `validateCanonicalMap` 在任何签名评估之前判定。
- **身份门槛按实际公钥材料计**：门槛统计 `keyMaterialId` 去重数；同一公钥材料的
  两张证书只算一个身份（`test/03-identity-threshold.test.ts`）。
- **管理域相斥**：`exclusionRules` 中声明的域对不得在同一见证集合中共现。
- **最少签名记录的全局见证**：对每个必须属性做全子集枚举（`enumerateSubsets`），
  先按子集大小（= 验证调用数）升序，同尺寸按记录编号向量升序，第一个可行子集即
  所求见证。
- **验证调用数**：每条通过结构校验的记录最多调用一次加密后端；结构缺陷记录不产生
  调用。`EvaluationReport.totalVerificationCalls` 给出全局调用数。
- **三类缺陷分别报告**：`UNKNOWN_ALGORITHM`、`UNBOUND_SCHEMA`、
  `AMBIGUOUS_REVERSE_MAP` 使用互不相同的诊断码，另有包身份/规范化版本/作用域
  不匹配与加密失败码（`test/05-diagnostics.test.ts`）。
- **重复路径别名**：两个区域路径可引用同一原字节而不构成歧义；但作用域不同的承诺
  不能复用——`scopeMatches` 要求记录实质覆盖的每个区域都属于其声明作用域
  （`test/06-alias-scope.test.ts`）。

## 测试结构

- `test/oracle.ts`：仅依据任务文字重写的独立朴素枚举 oracle，不导入求解器内部
  逻辑；每个场景都用它交叉对照求解结果。
- `test/01..07`：覆盖题目点名的全部情形——跨字段长度覆盖、折叠字符两侧分开签署、
  同一公钥两张证书、只在旧规范版本成立的见证、三类缺陷分别报告、别名作用域不可
  复用，以及 60 个随机小包的全子集枚举性质校验（最小性与排序）。
- `src/crypto.ts`：确定性模拟加密后端；真实宿主可实现 `CryptoBackend` 接口接入
  自己的密钥库，`Verifier` 构造函数接收任意后端。
