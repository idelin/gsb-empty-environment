# meter-ledger:有理数计量账本

只用 Python 标准库实现的采样曲线计量账本:采样、校准与积分查询之间
有真正的版本关系,更正以"撤销/替换"差量发布,支持崩溃恢复。

## 布局

- `meter_ledger/curve.py` — 采样曲线:`Fraction` 有理数时间与取值,
  阶梯 / 线性插值,断测区间(缺测只计长度、绝不当零),分段校准
  (`value' = scale * value + offset`,可从过去某时刻生效)。
- `meter_ledger/ledger.py` — 事件溯源账本:journal(事件 + 差量)、
  版本化查询、事务化更正、checkpoint、重放与去重。
- `demo_scenario.py` — 题面场景验证:30 → 36 → 断测 → 追溯校准,
  打印每个查询的撤销/替换及所用采样、校准版本。
- `demo_recovery.py` — 崩溃恢复演示:事务在"已撤销、未发布"处中断,
  重启重放不重复扣减。
- `tests/test_full_recompute.py` — 独立全量重算测试(逐单位步进的
  参考积分器,不依赖账本增量状态)及恢复/重放测试。

## 运行

```sh
python demo_scenario.py
python demo_recovery.py
python -m unittest discover -s tests -t .
```

## 设计要点

- **版本关系**:每次采样/断测变更递增 `sample_version`,每条校准规则
  递增 `calibration_version`;每份发布结果都记录当时所用的两个版本号。
- **局部重算**:更正(采样、断测、校准)只计算其影响区间,只重算与
  该区间相交的注册查询,不重算整段历史。
- **稳定 ID 与确认位置**:差量 ID 为 `<tx>:<query>:<kind>`(初始发布为
  `init:<query>:publish`),由事务与查询决定,崩溃前后保持一致;确认
  位置即差量在 journal 中的序号 `position`。
- **checkpoint 内容**:未闭合采样段(最后一个采样点,阶梯模式下延伸到
  +∞)、查询索引(区间 + 当前发布结果)、去重状态(已应用差量 ID 集合)、
  确认位置与事务计数器。checkpoint 只在事务外进行,写入用临时文件 +
  `os.replace` 保证原子性。
- **崩溃恢复**:重启后从 checkpoint 的确认位置重放 journal;已应用差量
  按稳定 ID 去重,不会重复扣减;发现未提交事务时,按相同稳定 ID 补发
  缺失的替换差量(已确认的自动跳过)并补写提交记录。
