# netsec 反回滚组件

供 C++20 固件更新组件调用的可导入反回滚（Anti-Rollback）组件。

## 设计

- **版本计数器**：`IVersionCounter` 抽象，两种后端——
  - `OtpFuseCounter`：一次性熔丝（OTP），版本 = 已熔断位数，物理不可回退；
  - `TrustedStorageCounter`：受信存储，主副本 + 冗余副本 + CRC-64 完整性校验。
- **安装约束**：镜像版本必须 >= 当前计数器值，否则拒绝（fail-closed）。
- **授权降级**：`DowngradeGrant` 绑定 (目标版本, 当前计数器值, 单调序号) 并需
  签名验证（`HmacAuthorizer`，可替换为 Ed25519/ECDSA 验签）；序号防重放；
  降级不降低熔丝计数器，仅作为已记录的显式例外。
- **回读与冗余校验**：OTP 双重回读、受信存储双副本 CRC 校验、写后回读确认；
  任何失败显式上报 `CounterError` 并记录审计，绝不静默降级。
- **审计**：所有放行/拒绝/计数器故障均写入 `IAuditLog`。

## 固件更新组件调用流程

```cpp
netsec::OtpFuseCounter counter(fuse_bank);
netsec::HmacAuthorizer authorizer(key);
MyAuditLog audit;
netsec::AntiRollback arb(counter, authorizer, audit, persisted_serial);

// 1. 安装前校验
auto d = arb.authorize_install(image.version, maybe_grant);
if (!d.allowed()) abort_update();          // 拒绝原因见 d.reason / d.counter_error

// 2. 烧写镜像 ...

// 3. 提交：推进计数器并回读确认；失败必须进入恢复流程，不得继续启动
if (!arb.commit(image.version)) enter_recovery();
```

## 构建与测试

```sh
cmake -B build && cmake --build build && ctest --test-dir build
```

测试覆盖：旧镜像拒绝、伪造/重放授权拒绝、授权降级、合法更新、
熔丝跳过检测、回读/冗余校验失败 fail-closed、计数器单调性。
