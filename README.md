# Pair-wise Delegation Authority

一个零外部依赖的 TypeScript 委托授权系统：主体只能在自身授权范围内委托子权限，支持
**委托深度、有效期限、不可再委托（sealed）标记、祖先撤销级联失效、完整授权链证明、
防循环委托**。策略引擎只读取请求中显式给出的属性，谓词限定为**有限集合成员判断、
整数条件与布尔组合**；身份仅由本地注册的 Ed25519 公钥表示，不接入任何外部身份服务。

使用 Node.js 内置测试运行器（`node --test`）与内置 TypeScript 类型擦除，无需安装
任何 npm 依赖。要求 Node.js ≥ 22.6（开发环境实测 v23.9.0）。

## 运行

```bash
node --version            # 需要 >= 22.6
npm test                  # 32 个内置测试
npm run demo              # 端到端运行样例
```

等价的直接命令：

```bash
node --test --experimental-strip-types test/policy.test.ts test/authority.test.ts test/scenarios.test.ts
node --experimental-strip-types examples/demo.ts
```

最近一次的完整输出保存在 `docs/test-output.txt` 与 `docs/demo-output.txt`。

## 代码结构

| 文件 | 职责 |
| --- | --- |
| `src/types.ts` | Grant、Policy、Proof、VerifyResult 等类型 |
| `src/keys.ts` | 内存 Ed25519 密钥目录（身份模型的全部：主体名 → 公钥） |
| `src/canonical.ts` | 确定性 JSON 序列化（键排序、数组排序），作为签名载荷 |
| `src/policy.ts` | 有限集合 / 整数 / 布尔策略的校验与只求值 |
| `src/clock.ts` | 时钟接口与可手动控制的 `ManualClock` |
| `src/authority.ts` | 根授权签发、委托、撤销、证明构建与链验证 |
| `src/errors.ts` | 带错误码的 `DelegationError` |
| `examples/demo.ts` | 14 步端到端样例 |
| `test/*.test.ts` | `node:test` 内置测试（32 个用例） |

## 授权链模型

- **根授权（root grant）** 自签，`parentId = null`、`issuer = subject`，是链上唯一
  允许的权力来源；不存在根的链一律拒绝。
- 每个非根授权由父授权的当前持有者签发：`issuer` 必须等于父授权的 `subject`，并携带
  父授权当时的签名（`parentSig`），形成密码学绑定的父子链接。
- 证明 `Proof` 是**从根到叶的完整有序签名链**，按原始呈现顺序校验，不能通过重排
  已截获的链接使证明生效。
- 每个签名是对授权文档的规范序列化做 Ed25519 签名；未知签发者、签名错误、内容与
  签名不符都拒绝。由本地存储构建的证明还会与存储绑定校验，无法凭空伪造一条
  “自洽”的链。

## 强制规则

委托签发时（`delegate`）：

- **范围不可扩大**：子授权的 `resources`、`actions` 必须是父授权集合的子集。
- **深度受限**：子授权 `maxDepth ≤ 父 maxDepth − 1`，默认取该上界；父深度为 0
  时不能再委托。
- **期限受限**：子授权 `[notBefore, expiresAt)` 必须包含于父授权窗口内；
  默认 `notBefore` 为当前时刻，期限不允许超过父期限。
- **不可再委托**：父授权 `sealed = true` 时拒绝继续委托；sealed 授权仍可正常使用。
- **策略只可收紧**：子授权生效策略是全链策略的合取（`and(parent.policy, child.policy)`），
  后代无法绕过祖先条件。
- **禁止循环**：拒绝自委托，拒绝把权限回授给链上已出现的任何主体；证明结构上
  再次检测重复授权与重复主体——循环永远不能成为权力来源。
- 委托前先完整验证父链当前有效（未过期、未被撤销），过期父授权上不能再签发新授权。

使用时（`authorize` / `verifyProof`）对每一条链链接检查：签名、锚点、链接绑定、
范围嵌套、深度、sealed、时间窗口包含、撤销状态；叶节点额外检查请求资源/动作范围，
并在**请求显式属性**上求合取后的策略。

- **祖先过期 → 后代失效**：即使叶授权自身仍在有效期内，任一祖先过期即拒绝。
- **撤销祖先 → 后代失效**：撤销是签发者的本地动作，验证时链上任意命中即拒绝。

## 策略语言

```ts
{ kind: "allowAll" } | { kind: "denyAll" }
{ kind: "attrIn", attr: "env", values: ["prod", "staging"] }   // 有限集合
{ kind: "intCmp", attr: "clearance", op: ">=", value: 2 }       // 整数条件
{ kind: "not", operand: Policy }
{ kind: "and" | "or", operands: Policy[] }                      // 布尔组合
```

- 求值只索引传入的属性对象自身属性（不读原型链，无任何查找钩子或外部调用）。
- 属性缺失、类型不符（如对非整数做整数比较）一律 fail-closed 返回 `false`。
- 集合必须是非空有限字面量；布尔组合至少一个操作数；结构在签发时校验。

## 样例摘要（`npm run demo`）

```
1-3  corp 根授权(3跳/1000s/clearance>=2) → alice(read,doc-a,+env∈{prod,staging}) → bob(sealed)
4    bob 以 clearance=3/prod 读取 doc-a                  ALLOW（完整三跳链）
5    clearance=1                                          DENY policy（祖先整数条件）
6    bob 访问 doc-b                                       DENY requestScope
7    bob 再委托                                           拒绝 sealed
8    alice 保留超出父级的深度                             拒绝 depth
10   carol 回授给 corp                                    拒绝 cycle
11   carol 自身到期                                       DENY time
12   alice 过期而 bob 窗口仍有效                          DENY time（祖先过期）
13   篡改序列化证明（叶节点加 doc-b）                     DENY sig + nesting
14   corp 撤销 alice                                      DENY revoked（bob 作为后代失效）
```
