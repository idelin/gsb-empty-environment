# cdc-schema-versions

按提交序号（commit_seq）生效的 schema 版本化变更解析。纯 Python 3.11
标准库实现，测试使用 unittest，数据来源为本地夹具（JSON），不接消息平台。

## 语义

- **按提交序号生效**：每个 schema 版本带 `effective_from`，事件按自身
  `commit_seq` 解析到「最后一个 `effective_from <= commit_seq`」的版本，
  与到达顺序无关。
- **旧事件晚到不错解**：晚到的旧事件 `commit_seq` 落在旧版本区间，仍用
  旧字段名解析，不会被新字段名错解。
- **改名映射稳定 ID**：字段改名（如 `user_name` → `display_name`）在新旧
  版本中保持同一 `stable_id`，解析结果以稳定 ID 为键，下游无感。
- **删字段与 null 区分**：after 映像中缺失的字段标记为 `DELETED` 哨兵；
  显式 `null` 保持为 `None`，二者严格区分。
- **缺定义不猜测**：字段名不在解析到的版本中定义时抛 `UnknownFieldError`；
  没有覆盖该 `commit_seq` 的 schema 时不猜测，进入有界缓冲。
- **未知 schema 有界缓冲**：`BoundedBuffer` 容量固定，溢出驱逐最旧条目并
  计数；注册新版本后自动重放缓冲事件，仍不可解析的保留。

## 变更记录格式（夹具）

```json
{
  "txn_id": "txn-099",
  "table": "users",
  "pk": {"user_id": 3},
  "commit_seq": 40,
  "before": {"user_id": 3, "user_name": "bob", "email": "b@x.com"},
  "after":  {"user_id": 3, "user_name": "bobby"}
}
```

schema 版本见 `fixtures/schema_changes.json`：v1（effective_from=0）含
`user_name`/`email`；v2（effective_from=100）将 `user_name` 改名为
`display_name`（稳定 ID 2 不变）、删除 `email`、新增 `age`。

## 运行

```bash
python3 run_demo.py                  # 演示样例
python3 -m unittest discover -s tests -v   # 测试
```

## 结构

- `cdc/schema.py` — `FieldDef` / `SchemaVersion` / `SchemaRegistry`
- `cdc/parser.py` — `ChangeParser`、`DELETED` 哨兵、错误类型
- `cdc/buffer.py` — `BoundedBuffer`（有界 FIFO，溢出驱逐最旧）
- `cdc/fixtures.py` — 本地夹具加载
- `fixtures/` — schema 变更与数据变更夹具
- `run_demo.py` — 端到端演示
- `tests/test_cdc.py` — 14 个 unittest 用例
