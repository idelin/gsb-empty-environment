"""两个有序可溢写流的 merge join。

语义
----
* 行是首元素为键的元组 ``(key, *cols)``。
* 多重集合语义：左侧键组有 m 行、右侧有 n 行时，输出 m*n 个配对，
  重复行完整保留。
* NULL（Python ``None``）按 SQL 规则处理：NULL 互不相等、永不匹配；
  排序时 NULL 视为最小，使 NULL 键彼此相邻成组。
* 左外连接（``how="left"``）中未匹配的左行以 NULL 填充右侧输出。
* 键组过大时任一侧（或两侧）都可溢写到磁盘的有界暂存区，
  溢写后仍可反复重绕迭代，因此超大组 × 超大组也能完成。
* 输入必须按键非递减序提供，违约立即抛出 ``OrderViolationError``，
  不会产出错误结果。
* 支持协作式取消（``CancellationToken``）。

输出为 ``(left_row, right_row_or_None)`` 二元组的惰性生成器。
"""

from __future__ import annotations

from collections.abc import Iterable, Iterator
from typing import Any

from .errors import CancellationToken, JoinCancelled
from .spool import BoundedSpool
from .stream import OrderedStream, key_sort_value


def _cmp_keys(left_key: Any, right_key: Any) -> int:
    lv, rv = key_sort_value(left_key), key_sort_value(right_key)
    if lv < rv:
        return -1
    if lv > rv:
        return 1
    return 0


def merge_join(
    left: Iterable[tuple],
    right: Iterable[tuple],
    how: str = "inner",
    memory_limit: int = 1000,
    tmp_dir: str | None = None,
    token: CancellationToken | None = None,
    right_arity: int | None = None,
) -> Iterator[tuple[tuple, tuple | None]]:
    """对两个按键有序的流执行 merge join。

    Parameters
    ----------
    left, right:
        行元组的可迭代对象，首列为连接键，均须按键非递减序提供。
    how:
        ``"inner"``（内连接）或 ``"left"``（左外连接）。
    memory_limit:
        每个键组允许驻留内存的最大行数，超出后溢写磁盘。
    tmp_dir:
        溢写文件目录；``None`` 使用系统临时目录。
    token:
        可选的取消令牌。
    right_arity:
        右表行宽（含键列），用于左外连接的 NULL 填充；
        缺省时从已见到的右行推断，右表全程为空时退化为单列 ``(None,)``。
    """
    if how not in ("inner", "left"):
        raise ValueError(f"how must be 'inner' or 'left', got {how!r}")

    def _impl() -> Iterator[tuple[tuple, tuple | None]]:
        ls = OrderedStream(left, name="left")
        rs = OrderedStream(right, name="right")
        spools: list[BoundedSpool] = []
        inferred_right_arity = right_arity

        def _check_cancel() -> None:
            if token is not None:
                token.raise_if_cancelled()

        def _read_group(stream: OrderedStream, key: Any) -> BoundedSpool:
            spool = BoundedSpool(memory_limit=memory_limit, tmp_dir=tmp_dir)
            spools.append(spool)
            while not stream.exhausted and _cmp_keys(stream.key, key) == 0:
                _check_cancel()
                spool.append(stream.current)
                stream.advance()
            spool.finish_write()
            return spool

        def _pad() -> tuple:
            width = inferred_right_arity or 1
            return (None,) * width

        try:
            ls.advance()
            rs.advance()
            if not rs.exhausted:
                inferred_right_arity = right_arity or len(rs.current)

            while not (ls.exhausted and rs.exhausted):
                _check_cancel()

                if ls.exhausted:
                    # 右侧剩余键组：内连接与左外连接都不产出。
                    _read_group(rs, rs.key)
                    continue
                if rs.exhausted:
                    cmp_result = -1
                else:
                    cmp_result = _cmp_keys(ls.key, rs.key)
                    inferred_right_arity = right_arity or len(rs.current)

                if cmp_result < 0:
                    # 仅左侧存在的键组。
                    key = ls.key
                    left_group = _read_group(ls, key)
                    if how == "left":
                        for lrow in left_group.rows():
                            _check_cancel()
                            yield lrow, _pad()
                    continue

                if cmp_result > 0:
                    # 仅右侧存在的键组：任何连接方式都不产出。
                    _read_group(rs, rs.key)
                    continue

                # 排序键相同。NULL 键虽相邻成组，但 NULL 永不匹配。
                key = ls.key
                null_key = key is None or rs.key is None
                left_group = _read_group(ls, key)
                right_group = _read_group(rs, key)

                if null_key:
                    if how == "left":
                        for lrow in left_group.rows():
                            _check_cancel()
                            yield lrow, _pad()
                    continue

                for lrow in left_group.rows():
                    matched = False
                    for rrow in right_group.rows():
                        _check_cancel()
                        matched = True
                        yield lrow, rrow
                    if how == "left" and not matched:
                        # 理论上不可达：非 NULL 的相等键组右侧必非空，
                        # 保留以防未来改动破坏不变量。
                        _check_cancel()
                        yield lrow, _pad()
        finally:
            for spool in spools:
                spool.close()

    return _impl()
