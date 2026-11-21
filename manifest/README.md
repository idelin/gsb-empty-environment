# manifest — 引导准入清单验证包

`prescreen.local/ps-d4797ec147/manifest` 是可导入的 Go 包，实现根密钥签名的
固件密钥清单（manifest）准入与固件启动授权。所有签名均为 Ed25519，摘要为
SHA-256，规范编码为确定性长度前缀二进制 TLV（见 `EncodeManifest`）。

## 信任与计票模型

- 根集合由宿主固定：`Host.Roots` 中每根含唯一 ID 与 Ed25519 公钥。
- 根签名覆盖完整规范编码；按根 ID 去重：
  - 未知根 ID 不计票、不报错；
  - 已知根签名验签失败 → `ErrInvalidRootSignature`；
  - 同一根 ID 出现两个不同签名 → `ErrConflictingRootSignatures`；
  - 去重后的已知根票数须达到 `Host.RootThreshold`。

## 清单准入规则（`Host.Admit`）

1. 结构校验：型号非空、时间窗左闭右开、阈值为正、keyID 与撤销 ID 无重复、
   密钥条目窗口为 `[StartGeneration, EndGeneration)`。
2. 型号必须与宿主一致；当前时间必须落在 `[NotBefore, NotAfter)`。
3. 固件阈值不得低于 `Host.MinFirmwareThreshold`。
4. 根签名计票达到根阈值。
5. 与可信存储中的已接受状态比较：
   - 较低代次 → `ErrStaleGeneration`（旧清单重放被拒，已撤销密钥无法复活）；
   - 同代次但摘要不同 → `ErrSameGenerationFork`（同代分叉被拒）；
   - 同代次同摘要 → 幂等返回，不产生新写入；
   - 更高代次须满足连续性：
     - 新撤销集合必须包含旧撤销集合全部项（否则 `ErrRevocationRollback`）；
     - 旧清单中仍有效（未撤销）且在新清单中仍有效的 keyID，公钥材料不得变化
       （否则 `ErrKeyMaterialChanged`）；被新清单撤销的 keyID 允许重绑材料，
       因为它在新清单中已不被信任。

## 固件准入规则（`Host.AdmitFirmware`）

- 固件签名输入为 `EncodeFirmwareSigningInput(型号, 镜像摘要, 版本, 清单代次)`。
- 只有当前清单中同时满足以下条件的密钥可计票：
  - keyID 不在撤销集合；
  - 用途包含 `firmware`；
  - 清单代次落在条目窗口 `[StartGeneration, EndGeneration)`。
- 公钥材料相同的别名 keyID（同一公钥指纹）只算一票；同一 keyID 的冲突签名
  使该 keyID 整体作废；未知 keyID 与坏签名一律不计票。
- 去重后有效票数达到清单内阈值 `t` 才放行；固件绑定的清单代次必须与当前
  可信代次一致（`ErrFirmwareGenerationMismatch`）。

## 原子持久化与崩溃恢复

- `Store` 保存最高代次、清单摘要、接受时间与完整清单体，二者同记录提交。
- `FileStore` 采用「写临时文件 → fsync → 原子 rename」：rename 前崩溃只留下
  临时文件，正式文件仍是上一代完整状态，杜绝「新代次配旧内容」。
- 加载时校验魔数、整体校验和、摘要与清单体一致性，以及代次与清单体一致性，
  任何篡改都返回 `ErrCorruptState`。
- `MemoryStore.SetSaveFault` 与 `FileStore.BeforeRename` 提供故障注入点，用于
  测试最后一笔持久化失败。

## 测试

`go test ./...` 使用真实 Ed25519 密钥与签名，覆盖：旧清单重放、别名密钥计票、
撤销回退、阈值降低、同代分叉、根签名冲突/未知根、时间窗边界，以及最后一笔
持久化失败与文件存储防篡改。
