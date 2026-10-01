# strided_tensor

纯标准库（Python 3.11）实现的 CPU 小张量 stride/offset 视图，不依赖任何
自动微分或张量库。测试使用 `unittest`。

## 文件

- `strided_tensor.py` — 实现：`Tensor` / `Storage` / 重叠判定 / 写保护
- `test_strided_tensor.py` — 23 个 unittest 用例
- `demo.py` — 运行样例

## 设计要点

- **视图模型**：`Tensor = (storage, shape, strides, offset)`，所有视图共享
  `Storage`。支持转置、负步长翻转（`flip` / `t[::-1]`）、非连续切片
  （`t[::2, 1::2]`）、零步长广播（`broadcast_to`）、空维（`numel == 0`，
  不寻址、不参与重叠、拷贝为空操作）。
- **越界检查**：按视图实际寻址区间 `[lo, hi]`（由 stride 符号累计）校验，
  不是只看元素总数——`shape=(3,), stride=4, len=8` 会被拒绝。
- **重叠判定（三态 Tri.NO / YES / UNKNOWN）**：
  - 内部重叠：快速充分条件（|stride| 升序后每项严格大于前面所有步长的
    可达跨度）不满足时，用精确有限求解——可达地址集合 DP，像集大小
    小于元素数即碰撞；超 `SOLVER_BUDGET` 返回 UNKNOWN。
  - 两视图交叠：快速充分条件（包围地址区间不相交）不足时，精确求真实
    地址集合的交集；超预算返回 UNKNOWN，写路径按不安全处理。
- **写路径**：目标内部重叠（如广播视图）直接 `OverlapError`；源/目标
  交叠时快照语义（先整体物化源再写）；判定 UNKNOWN 拒绝不确定覆盖；
  `borrow()` 上下文期间同一 storage 的所有写入抛 `BorrowError`。
- **reshape**：连续视图直接改形（共享存储）；转置/负步长等非连续视图
  默认 `ReshapeError`，`copy=True` 时复制后改形。

## 验证方式

- 数值正确性：与独立的纯 Python 参照（嵌套 list 的转置/翻转/切片）逐项
  对比，含随机用例。
- 重叠判定：对随机小张量穷举地址集合（所有逻辑索引对的地址碰撞 /
  两视图地址集合求交）核验三态判定，共 800 例，不只比较包围字节区间。

## 运行

```sh
python3.11 -m unittest test_strided_tensor -v   # 23 tests, OK
python3.11 demo.py
```
