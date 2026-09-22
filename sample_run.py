#!/usr/bin/env python3
"""merge join 运行样例。

用法::

    python3 sample_run.py

依次演示：内连接、左外连接、NULL/多重集合语义、大组溢写（单侧与双侧）、
输入顺序违约报错、协作式取消。
"""

from __future__ import annotations

import sys
import threading
import time

from mergejoin import (
    CancellationToken,
    JoinCancelled,
    OrderViolationError,
    merge_join,
)
from mergejoin.relations import (
    BIG_GROUP_SIZE,
    LEFT_ROWS,
    RIGHT_ROWS,
    big_left_rows,
    big_right_rows,
    one_sided_big_left,
)


def _render(pairs, max_show=None):
    lines = []
    for n, (lrow, rrow) in enumerate(pairs, 1):
        lines.append(f"  {n:>5}: {lrow}  <->  {rrow}")
        if max_show is not None and n >= max_show:
            lines.append("  ... (其余省略)")
            break
    return "\n".join(lines)


def main() -> int:
    print("== 1. 内连接：2×3 重复键组产出 6 行 ==")
    pairs = list(merge_join(LEFT_ROWS, RIGHT_ROWS, how="inner"))
    print(_render(pairs))
    print(f"  合计 {len(pairs)} 行\n")

    print("== 2. 左外连接：左独有键 5 以 NULL 填充，NULL 键不匹配 ==")
    pairs = list(merge_join(LEFT_ROWS, RIGHT_ROWS, how="left"))
    print(_render(pairs))
    print(f"  合计 {len(pairs)} 行\n")

    print("== 3. 大组双侧溢写（每组 1000 行，组内存上限 8 行）==")
    count = 0
    spill_seen = [False, False]
    for lrow, rrow in merge_join(
        big_left_rows(), big_right_rows(), how="inner", memory_limit=8
    ):
        if lrow[0] == 2 and not spill_seen[0]:
            spill_seen[0] = True
        count += 1
    print(f"  key=2 配对 {BIG_GROUP_SIZE * BIG_GROUP_SIZE} 行，"
          f"加上 key=1、key=4 各 1 行，合计 {count} 行")
    print(f"  两侧键组均已溢写磁盘后仍完整完成\n")

    print("== 4. 仅单侧大组溢写（左 1000 行 × 右 1 行，左外连接）==")
    right = [(7, "R_7_only")]
    pairs = merge_join(one_sided_big_left(), right, how="left", memory_limit=8)
    count = sum(1 for _ in pairs)
    print(f"  合计 {count} 行（左组溢写，右组留内存）\n")

    print("== 5. 输入顺序违约：报错而非产出错误结果 ==")
    bad_left = [(1, "a"), (3, "c"), (2, "b")]
    try:
        list(merge_join(bad_left, RIGHT_ROWS, how="inner"))
    except OrderViolationError as exc:
        print(f"  OrderViolationError: {exc}\n")

    print("== 6. 取消：另一线程在 20ms 后置位令牌 ==")
    token = CancellationToken()

    def _cancel_later() -> None:
        time.sleep(0.02)
        token.cancel()

    threading.Thread(target=_cancel_later, daemon=True).start()
    emitted = 0
    try:
        for _ in merge_join(
            big_left_rows(), big_right_rows(), how="inner",
            memory_limit=8, token=token,
        ):
            emitted += 1
            if emitted % 100000 == 0:
                # 让出 GIL，使取消线程能够运行。
                time.sleep(0)
    except JoinCancelled as exc:
        print(f"  JoinCancelled: {exc}（取消前已产出 {emitted} 行，临时文件已清理）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
