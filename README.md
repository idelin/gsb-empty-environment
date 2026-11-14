# 固件启动链逐级验证组件

`netsec` 是一个可被 C++20 启动链组件直接导入的静态库。它把多级固件启动建模为
一条逐级委托的签名与度量链，而不是"只验末端镜像"：

- 信任锚（`TrustAnchor`）固定第一级镜像的 Ed25519 公钥、链 ID 与根阶段身份。
- 第 `i` 级镜像的清单由第 `i-1` 级清单声明的 `next_authority` 验签；第 0 级由
  信任锚验签。所以替换任意中间级，都必须同时伪造其前驱的签名。
- 每个清单签名覆盖：阶段身份、序号、版本、下一级身份、下一级公钥、下一级最低
  版本、载荷长度和载荷 SHA-256。
- 每级的度量值为
  `SHA-256(上一级度量 || 序号 || 版本 || 清单 || 签名)`，根度量绑定信任锚指纹。
  `AttestationProof` 保存完整逐级 transcript，可脱离原始镜像再次验证。
- 版本同时受持久化回滚下限和父级清单中签名固定的 `min_next_version` 约束。
- 只有整条链验证成功后才提交回滚状态；任何失败都不会推进下限。

密码学实现自包含（SHA-256、SHA-512、RFC 8032 Ed25519），不依赖 OpenSSL。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法四级链、中间阶段替换后复用合法末端签名、缺少中间级、版本回退、
父级版本策略、载荷篡改、错误信任锚、回滚状态原子提交、证明篡改与序列化回读，
以及 SHA-256/SHA-512/Ed25519 RFC 8032 测试向量。

## 基本用法

```cpp
#include "netsec.hpp"

netsec::SigningKey root(seed);                 // 仅用于烧录/镜像签名
netsec::TrustAnchor anchor =
    netsec::TrustAnchor::pinned(/*chain_id=*/1, "BL1", root.public_key());

netsec::ChainPolicy policy{
    /*chain_id=*/1, {"BL1", "BL2", "BL31", "APP"}, /*max_stages=*/16,
    /*require_stage_ids=*/true};

netsec::InMemoryRollbackStore rollback;         // 产品中替换为持久化实现
netsec::BootChainVerifier verifier(anchor, policy, rollback);

const netsec::VerificationReport report = verifier.verify(stages);
if (!report.ok()) {
    // report.error, report.failed_stage, report.detail
    return false;
}

std::string detail;
if (!netsec::verify_proof(report.proof, anchor, &detail)) {
    return false;
}
```

构造镜像时，`BootStage::create(manifest, payload, predecessor)` 会计算载荷摘要并用
`predecessor` 签名。第 0 级的 `predecessor` 是信任锚私钥；第 `i` 级的
`predecessor` 必须对应第 `i-1` 级清单中的 `next_authority`。阶段清单中的
`next_stage_id`、`next_authority`、`min_next_version` 是父级对子级的签名策略。

末端阶段的 `next_stage_id` 与 `next_authority` 必须为空/零，`min_next_version`
必须为零。链在末端之前结束、末端之后多出阶段、序号跳变或身份重复都会被拒绝。

## 攻击场景

- **中间替换 + 末端签名复用**：替换 `BL2` 后保留合法 `APP` 签名。`APP` 单独仍能
  通过旧 `BL31` 公钥验证，但完整链在 `BL2` 处因前驱签名失败而拒绝。
- **中间缺失**：按策略缺级或序号跳变，分别返回 `missing_stage` /
  `stage_index_mismatch`；末端阶段公告的后继未出现也返回 `missing_stage`。
- **版本回退**：任何阶段的版本低于 `RollbackStore` 下限返回 `version_rollback`；
  低于父级签名固定的最低版本返回 `version_policy_violation`。

## 接口边界

- `SigningKey` 用于镜像生产/测试；启动链运行时只需要公钥、验签和回滚存储。
- `RollbackStore::floor` 读取下限，`commit` 必须在成功路径上持久化。
  `BootChainVerifier` 在整条链成功前不会调用 `commit`。
- `AttestationProof::canonical_bytes()` 产生确定性二进制日志；
  `AttestationProof::parse()` 可回读，再交给 `verify_proof()` 使用固定信任锚验证。
- 任意清单截断、尾随字节、非规范 Ed25519 点/标量或签名都会被拒绝。
