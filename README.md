# 安全的计划选择：局部 TopK 归并 + 可证明前提的下推

纯 Python 3.11 标准库实现（不调用任何 SQL 引擎），显式声明 NULL（三值逻辑）
与多重集合（bag）语义。

## 文件

- `engine.py` — 关系算子：Scan(分区)/Filter/Join(inner,left)/Aggregate/OrderLimit/TopKMerge
- `planner.py` — 三条改写规则，每条先构造性验证前提，不满足则保留原计划
- `synth_data.py` — 本地合成关系表（含 NULL、重复键、并列值、3 个分区）
- `demo.py` — 运行样例；`test_planner.py` — unittest 测试（22 例）

## 规则与前提

1. **Sort+Limit → 局部 TopK + k 路归并**：每分区必须取 `offset+limit` 行
   （证明：全局前 offset+limit 名的行在其分区内也必在前 offset+limit 名内），
   归并后再施加 offset/limit；并列按 `_seq`（原始输入序）稳定决胜。
2. **外连接谓词下推**：谓词仅引用保留侧列可直接下推；引用补 NULL 侧时，
   对全 NULL 扩展行求值，非 TRUE 即为 NULL 拒绝谓词（LOJ 退化为内连接），
   否则保留原计划。
3. **聚合下推**：要求分组/聚合列全在左侧、连接键在分组列中、右表连接键
   唯一（不放大）；内连接还需外键成立（不丢失）。前提不满足时保留原计划。

## 运行

```sh
python3.11 demo.py            # 运行样例
python3.11 -m unittest -v     # 测试
```
