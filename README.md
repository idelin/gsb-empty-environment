# 预计算合约地址在部署后换了代码

授权系统提前批准一个确定性部署地址，攻击者可让该地址先无代码通过检查，再部署不同逻辑或在测试模型中销毁后重建。为TypeScript链上宿主实现题内确定性地址与代码身份包：地址=后20字节SHA256(0xff||deployer20||salt32||SHA256(initCode))，所有字段定长；部署记录绑定chainID、deployer、salt、initCode摘要、预期runtimeCode摘要、构造参数摘要和部署代次。预授权只能针对完整记录签发，不能只存地址；实际部署回调返回runtimeCode、创建交易ID和块高，只有地址及runtime摘要全匹配才激活权限。题内链模型允许Destroy把代码清空但地址代次递增，之后同地址再次Deploy必须新记录和新授权，旧授权永久失效；同一代次代码不可变。授权回执本身不可跨chainID复制；外部Call前每次核对当前代码摘要和代次，执行中的代码变化使调用失败。搜索待审实现中仅按地址缓存授权导致不同runtimeCode成功执行敏感回调的最短Deploy/Destroy/Deploy序列。覆盖salt相同但initCode不同、构造参数遗漏、跨chainID、部署失败后重试及代码为空，明确该模型不声称复刻特定链当前自毁规则。

## 实现结构

- `src/bytes.ts`：定长字节工具（SHA256、拼接、hex、u64be、长度断言）。
- `src/record.ts`：确定性地址派生（`deriveAddress`，后 20 字节
  `SHA256(0xff‖deployer20‖salt32‖SHA256(initCode))`）、部署记录
  （`DeploymentRecord`，168 字节定长规范编码）、授权回执
  （`Authority`/`Receipt`，tag 绑定 chainID 与记录摘要，不可跨链复制）。
- `src/chain.ts`：题内链模型。`deploy` 返回含 runtimeCode、创建交易 ID、
  块高的回调；`destroy` 清空代码并使地址代次递增；同一代次地址被占用时
  禁止再次 Deploy（代码不可变）；`runtimeFor` 使 runtimeCode 同时取决于
  initCode 与构造参数。
- `src/host.ts`：`SecureHost`（预授权只针对完整记录；回调中地址与
  runtime 摘要全匹配才激活；Call 前后各核对一次代码摘要与代次，执行中
  代码变化即失败；Destroy 后旧授权永久失效）与 `VulnerableHost`
  （待审对照实现：仅按地址缓存授权，存在缺陷）。
- `src/search.ts`：在待审实现上 BFS 搜索最短攻击序列，结果为
  `deploy-honest → destroy → deploy-evil → call`（深度 3 内无解）。
- `test/model.test.ts`：16 个用例，覆盖 salt 相同 initCode 不同、构造
  参数遗漏、跨 chainID、部署失败后重试、代码为空、Destroy 代次语义、
  执行中代码变化、最短攻击序列及安全宿主的对照阻断。

## 运行

```sh
npm test
```

## 免责声明

本仓库是题内链模型：Destroy 清空代码并使地址代次递增，同一代次代码
不可变。本模型**不声称复刻任何特定链当前的自毁（selfdestruct）规则**。
