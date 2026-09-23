# CDC 一致脱敏引擎（Python 3.11，仅标准库）

从本地 JSON 夹具读取 CDC 变更（事务 ID、表、主键、前后值、提交序号），
按版本化字段策略对前后值一致脱敏，不接任何消息平台。

## 运行

```bash
python3.11 run_sample.py                  # 端到端样例（夹具 fixtures/sample_stream.json）
python3.11 -m unittest discover -s tests -v   # 14 个单元测试
```

## 设计要点

- **一致脱敏**：HMAC-SHA256 按 `域 + 值` 确定性假名化；更新前后同值映射相同。
- **主外键稳定映射**：`FieldPolicy.domain` 让主键与其外键共享假名域，跨表可关联。
- **删除可定位**：删除事件只含旧键时，按事件 `commit_seq` 对应的历史策略与密钥版本
  解析（`PolicyHistory.at`），不用当前密钥猜；归档的旧版本映射表保留可查。
- **策略热更新**：`Engine.reload_policy` 将主外键及前后值映射整体原子迁移到新假名
  版本；任一映射失败抛 `MigrationError`，保留旧存储、旧策略与旧输出版本。
- **schema 演进**：新版本 `PolicySet` 可增删字段策略与敏感字段声明。
- **严格模式**：出现的字段无策略（含新增敏感字段）即拒绝事件，错误日志只记
  表名/字段名，绝不写原值。
- **不可见字段**：`invisible` 策略字段从输出彻底剔除，错误路径同样不泄漏。
- **回放验证**：`Engine.commit_replay` 在克隆引擎上回放合成引用图，校验跨版本
  引用一致与不可见字段非干扰；失败则保留旧输出与旧版本号。

## 布局

- `cdc_pseudonym/policy.py` — 策略模型、版本历史
- `cdc_pseudonym/pseudonymizer.py` — 版本密钥 HMAC 假名化
- `cdc_pseudonym/store.py` — 映射存储与原子迁移
- `cdc_pseudonym/engine.py` — 事件处理、热更新、回放提交
- `cdc_pseudonym/replay.py` — 引用图回放验证
- `cdc_pseudonym/fixtures.py` — 本地夹具加载
- `fixtures/sample_stream.json` — 样例流（v1→v2 热更新、历史删除、严格拒绝）
- `tests/test_cdc_pseudonym.py` — unittest 套件
