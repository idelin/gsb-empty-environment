# 双时态计价回放 (Bitemporal Price Ledger)

同时按**业务生效时间**与**系统知悉时间**查询的价格账本。纯 Python 3.11 标准库实现, 无第三方依赖。仓库内所有示例数据均为合成数据。

## 语义规则

- 价格区间为**左闭右开** `[valid_from, valid_to)`, 时间为整数刻度, 金额为**整数分**(`amount_cents >= 0`)。
- 同 `(product, currency)` 的重叠规则, 按 `(known_from, revision)` 取**最大**者胜出(先比知悉时间, 再比修订号)。
- 规则自 `known_from` 起对查询可见; **撤销**(`revoke`)只影响 `known_time >= 撤销known_from` 的查询, 撤销前的历史查询结果不变。
- 同 `(product, currency, revision)` 再次插入: 内容相同则幂等(返回 `inserted:false`), **内容不同则拒绝**(报错)。
- `explain` 输出本次查询涉及的**全部规则**作为证据(含是否已撤销、是否被选中)。
- 查询结果按 `(product, currency, business_time, known_time)` 缓存; 插入迟到修订或撤销时, **只失效**同商品同币种、`known_time >= 记录known_from` 且 `business_time` 落在记录业务区间内的缓存项。

## 运行

```bash
python3.11 bitemporal_pricing.py < commands.jsonl
```

## 输入格式 (JSON Lines, 每行一个对象)

| op | 字段 | 说明 |
|---|---|---|
| `add` | `product, currency, valid_from, valid_to, amount_cents, revision, known_from` | 插入价格规则 |
| `revoke` | `product, currency, revision, known_from` | 撤销某修订, 自 `known_from` 起生效 |
| `query` | `product, currency, business_time, known_time` | 查询胜出价格 |
| `explain` | 同 `query` | 查询并输出全部证据 |
| `cache_stats` | 无 | 查看缓存大小与键 |

## 输出格式 (每行一个 JSON, 与输入一一对应)

- `add` → `{"ok":true,"inserted":true|false}`
- `revoke` → `{"ok":true}`
- `query` → `{"ok":true,"amount_cents":<int|null>,"rule":<object|null>}`
- `explain` → `{"ok":true,"amount_cents":...,"winner":{...},"evidence":[{"rule":{...},"revoked_at_known_time":bool,"selected":bool},...]}`
- `cache_stats` → `{"ok":true,"size":<int>,"keys":[[product,currency,business_time,known_time],...]}`
- 任何错误 → `{"ok":false,"error":"<消息>"}`(仅该行失败, 后续行继续处理)

## 验收场景 (合成数据)

价格 10 元(1000 分)在业务 `[0,20)` 有效、知悉时间 2; 价格 15 元(1500 分)在业务 `[5,10)` 有效、知悉时间 8:

```bash
printf '%s\n' \
'{"op":"add","product":"P1","currency":"CNY","valid_from":0,"valid_to":20,"amount_cents":1000,"revision":1,"known_from":2}' \
'{"op":"add","product":"P1","currency":"CNY","valid_from":5,"valid_to":10,"amount_cents":1500,"revision":2,"known_from":8}' \
'{"op":"query","product":"P1","currency":"CNY","business_time":7,"known_time":7}' \
'{"op":"query","product":"P1","currency":"CNY","business_time":7,"known_time":8}' \
'{"op":"query","product":"P1","currency":"CNY","business_time":10,"known_time":8}' \
| python3.11 bitemporal_pricing.py
```

结果: 业务7/知悉7 → 1000; 业务7/知悉8 → 1500; 业务10 → 1000(`[5,10)` 右开, 不含 10)。

## 测试

```bash
python3.11 -m unittest test_bitemporal_pricing -v
```

覆盖: 验收边界、左闭右开区间、重叠规则裁决(知悉时间→修订号)、同修订号冲突拒绝与幂等、撤销的时间局部性、证据输出、迟到修订/撤销的精确缓存失效、CLI 端到端。

## 文件

- `bitemporal_pricing.py` — 账本实现 + CLI 入口
- `test_bitemporal_pricing.py` — unittest 测试
