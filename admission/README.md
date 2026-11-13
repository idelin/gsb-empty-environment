# admission — 密钥服务器更新包准入过滤

导入路径：`example.org/netsec/q0762/admission`

抵御第三方认证签名泛滥（certificate flooding）：调用方提交已解析的公钥包、
签名者白名单和验证预算，密码学验证通过 `VerifyFunc` 回调完成，本包只负责
调度验证与结构裁剪。

## 准入规则

- 每个用户标识仅保留一条可验证的**最新自签名**；自签名无法验证（伪造/畸形
  或无自签名）时整个用户标识丢弃。
- 第三方签名必须来自 `Whitelist` 且回调验证通过；每个用户标识最多保留
  `MaxThirdPartyPerUID = 50` 条，按创建时间保留较新者。
- 每个公钥最多 `MaxUIDs = 10` 个用户标识、`MaxSubkeys = 20` 个子密钥，
  超出按创建时间保留较新者；超限只裁剪，不拒绝整个公钥。
- 裁剪后序列化体积超过 `MaxPacketSize = 1 MiB` 时返回 `ErrPacketTooLarge`。
- 所有签名以 SHA-256 内容摘要去重；被丢弃签名的摘要进入墓碑集合。

## 预算语义（`Options.Budget`）

- `Budget > 0`：本次最多执行该数量的回调验证，防止垃圾签名耗尽 CPU。
- `Budget == 0`：不做任何验证；未验证而丢弃的签名**不写墓碑**，允许后续
  预算充足时重试。
- `Budget < 0`：不限制验证次数。

非白名单签名在验证前即被拒绝，不消耗预算。

## 合并与墓碑（`Store.Merge` / `MergeInto`）

- 合并按指纹聚合，用户标识按 Raw 归并、签名按摘要去重，子密钥按 Raw 去重。
- 一旦某签名被丢弃并写入墓碑，后续任何更新都无法再把它加回
  （`TestMergeTombstonePreventsResurrection`）。
- 主指纹冲突返回 `ErrFingerprintMismatch`。

## 测试覆盖

`go test ./admission/` 覆盖：上万条第三方签名灌入、伪造自签名、子密钥/UID
上限、重复提交去重、验证预算、1 MiB 体积边界、合并幂等与回灌防护。
