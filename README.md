# pseudonym-service

基于标准 HMAC-SHA256 的域隔离假名服务。仅使用 Python 3.11 标准库，仅处理合成数据。

## 定义与参数

- **主体 (subject)**：调用方提供的合成标识符，如 `subject-000`。原始主体只用于内存派生，绝不写入审计日志或持久化文件。
- **域 (domain)**：隔离命名空间，如 `billing` / `analytics`。同域同主体假名稳定；不同域假名不同。
- **密钥 (key)**：调用方提供的字节串，按版本管理（`KeyRing`），最少 16 字节。密钥不写日志、不落盘；`repr` 已脱敏。
- **派生**：`digest = HMAC-SHA256(key_v, INFO || len(domain)||domain || len(subject)||subject || len(epoch)||epoch)`，假名格式 `ps:v{version}:{hexdigest}`。长度前缀编码保证三元组无歧义。未自行发明任何密码算法，唯一原语是标准库 `hmac` + `hashlib.sha256`。
- **碰撞检测与解决**：派生结果若已被其他主体占用（或测试钩子强制注入），确定性地递增 `epoch` 重新派生，直到唯一。同样输入产生同样解决路径，可重放验证。
- **轮换**：`rotate_key(new_key)` 为全部已知主体在新版本下预计算假名与旧→新映射，在单个临界区内原子提交；提交前取消（`cancel_check` 抛出 `CancelledError`）或失败不留任何部分状态。旧记录保留，旧引用可解析（不悬空），`resolve_current` 沿映射指向新版本。映射显式可查，但新旧版本不混用：新签发只用当前版本。
- **持久化与恢复**：`save`/`load` 只保存注册表与映射（不含密钥）；恢复时校验密钥环版本匹配并执行 `verify_consistency()`（索引一致、映射两端无悬空、记录引用的密钥版本均存在）。
- **并发**：所有状态变更在单把 `threading.RLock` 内完成；并发同主体请求得到同一假名。

## 运行

```bash
python3.11 demo.py                 # 运行样例
python3.11 -m unittest discover -s tests -v   # 16 个测试
```

## 隐私声明

本服务提供的是确定性的、密钥持有的域隔离假名化（pseudonymization），不是匿名化。统计实验（如碰撞频率观测）不作为任何形式化隐私证明；未提供差分隐私或等价的形式保证。仅用于合成数据。
