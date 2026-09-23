# 变更驱动的增量左外连接视图

Python 3.11+，仅标准库（`collections.Counter`、`dataclasses`、`unittest`）。
输入是**本地夹具**构造的表变更（事务ID、表、主键、前值/后值、提交序号），
**不接任何消息平台**；输出是按事务边界发布的左外连接视图净增量。

## 语义

- 左行先到、右侧无匹配 → 发布一条 `matched=False` 的 **NULL 扩展行**。
- **首个右匹配到来** → 撤销该 NULL 扩展行，发布匹配行。
- 右匹配多条 → 每个 `(左行, 右行)` 组合各产生一行（Counter 重数，多对多正确）。
- 删除一条右匹配后**仍有其它匹配** → 只删对应行，**不补 NULL**。
- **最后一个匹配被删除** → 删除匹配行并恢复 NULL 扩展行。
- 同一事务内左、右表的多次变化合并为一个多重集增量计数器，
  中间的 NULL 撤销/恢复相互抵消，只按提交后的**最终一致结果**发布一次净增量。

## 文件

- `view.py`：`Change`、`Publication`、`LeftOuterJoinView`（索引 + Counter 多重集）。
- `fixtures.py`：本地夹具——部门（左）LEFT JOIN 员工（右），9 个事务的变更时间线。
- `demo.py`：逐事务打印增量发布与最终视图。
- `test_view.py`：30 个 unittest，覆盖上述全部语义与非法变更拒绝。

## 运行

```bash
python3 demo.py
python3 -m unittest test_view -v
```

## 样例输出（demo.py）

```
[seq=1] 事务 T1  (1 条变更) department:1 INS
      + Engineering        | <NULL>
[seq=2] 事务 T2  (1 条变更) employee:101 INS
      + Engineering        | Alice
      - Engineering        | <NULL>
[seq=3] 事务 T3  (1 条变更) employee:102 INS
      + Engineering        | Bob                 # 左一右二 -> 输出 2 行
[seq=4] 事务 T4  (3 条变更) employee:101 DEL; department:2 INS; employee:201 INS
      + Human Resources    | Carol               # 删 Alice 后仍有 Bob -> 不补 NULL
      - Engineering        | Alice
[seq=5] 事务 T5  (1 条变更) employee:102 DEL
      + Engineering        | <NULL>              # 最后匹配删除 -> 恢复 NULL
      - Engineering        | Bob
[seq=6] 事务 T6  (2 条变更) department:1 UPD; employee:103 INS
      + Engineering Team   | Dan                 # 同事务左右联动，只发最终匹配行
      - Engineering        | <NULL>
[seq=7] 事务 T7  (2 条变更) employee:201 UPD; department:3 INS
      + Human Resources    | <NULL>              # Carol 调到 SALES，HR 恢复 NULL
      + Sales              | Carol
      - Human Resources    | Carol
[seq=8] 事务 T8  (2 条变更) employee:103 DEL; employee:103 INS
      (净增量为空，无行发布)                      # 删后插同主键，净抵消
[seq=9] 事务 T9  (2 条变更) employee:301 INS; employee:201 DEL
      + Sales              | Frank               # 删 Carol 后仍有 Frank -> 不补 NULL
      - Sales              | Carol

最终视图（按重数展开，共 3 行）
   Engineering Team   | Dan
   Human Resources    | <NULL>
   Sales              | Frank
```

## 测试结果

```
Ran 30 tests in 0.002s

OK
```
