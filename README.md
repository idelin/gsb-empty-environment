# 远程执行验收模块

对远程执行器上报的产物清单做验收，并实现内容缺失补取状态机。

## 状态机

```
awaiting-manifest --(清单匹配)--> fetching --(分块齐套)--> verifying
verifying --(校验通过)--> accepted        （终态）
verifying --(校验失败)--> failed          （终态，清理该代次全部已收内容）
failed --(expectAttempt 升级代次)--> awaiting-manifest
```

## 不变量

1. **严格匹配**：只接受 `inputDigest`、`toolVersion`、`attempt` 与当前期待上下文逐项一致的清单，否则 `context-mismatch`。
2. **重复清单幂等**：同一清单（内容指纹一致）重复上报返回 `duplicate-manifest`，不打断进行中的补取；同代次不同内容的清单拒绝替换。
3. **代次单调**：`acceptedAttempt` 只增不减。旧代次迟到的清单与成功回报（`notifyRemoteSuccess`）一律拒绝（`stale-attempt`），不得覆盖已接受的新代次；`expectAttempt` 不允许回退。
4. **失败即清理**：任一分块或整文件 sha256 校验失败，立即按代次清理全部已收内容并进入 `failed` 终态。

## 目录

- `src/types.ts` — 清单、上下文、状态等类型定义
- `src/acceptance.ts` — 验收状态机 `AcceptanceController`
- `src/store.ts` — 按代次隔离的内容分块存储（含 `purgeAttempt` 清理）
- `src/digest.ts` — sha256 与清单内容指纹
- `src/fixtures.ts` — 由真实内容构造清单/分块的辅助工具
- `src/simulate.ts` — 模拟：重复清单、缺块补取、校验失败清理、旧代次覆盖防护
- `test/acceptance.test.ts` — 12 个用例的测试套件（自带极简 runner）

## 运行

```sh
npm install        # 安装 typescript（唯一 devDependency）
npm test           # 编译并运行测试
npm run simulate   # 编译并运行场景模拟
```

> 离线环境说明：本工作区无法访问 npm registry，`node_modules/` 中的
> `typescript` 与 `@types/node` 是指向本机其他仓库的符号链接，仅用于本地验证；
> 在正常环境中 `npm install` 即可，无需符号链接。
