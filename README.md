# CDC Schema 版本化解析

纯 Python 3.11 标准库实现，无消息平台依赖；变更事件来自本地 JSON 夹具。

## 规则

- **按提交序号生效**：每个 schema 版本带 `effective_from`，事件按自身 `commit_seq`
  解析到「最后一个 `effective_from <= commit_seq`」的版本。
- **旧新事件各按各版本**：晚到的旧事件（seq < 新版本生效点）仍用旧字段名解析；
  反之旧事件携带新字段名会被拒绝。
- **改名映射稳定 ID**：字段以 `field_id` 为稳定标识，改名（`full_name` →
  `display_name`）后下游仍看到同一个 id。
- **删字段 ≠ 值 null**：显式 `null` 解析为 `None`；schema 定义了但 payload 未携带
  的字段标记为 `ABSENT` 哨兵；schema 版本间被删除的字段列入 `deleted_field_ids`。
- **缺定义不猜测**：payload 出现 schema 未定义的字段名 → `UnknownFieldError`；
  类型不匹配 → `FieldTypeError`，不做任何推断或强转。
- **未知 schema 有界缓冲**：无法解析版本的事件进入 `BoundedBuffer`（容量上限，
  溢出抛 `BufferOverflowError`）；schema 注册后按表回放，仍无法解析的重新缓冲。

## 结构

- `cdc/schema.py` — `SchemaRegistry` / `SchemaVersion` / `FieldDef`
- `cdc/events.py` — `RawChangeEvent`（txid、表、主键、前后值、提交序号）、`ParsedChange`、`ABSENT`
- `cdc/parser.py` — `ChangeParser`、`BoundedBuffer` 及错误类型
- `cdc/loader.py` — 本地夹具加载
- `fixtures/` — schema 与事件样例
- `main.py` — 运行样例；`tests/` — unittest 套件

## 运行

```sh
python3 main.py                              # 演示
python3 -m unittest discover -s tests -v     # 测试
```
