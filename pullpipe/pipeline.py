"""管线外壳：绑定取消令牌、驱动拉取、保证资源释放。"""

from __future__ import annotations

from typing import Callable, Iterator

from .batch import Batch, Row
from .operator import CancellationToken, Operator


class Pipeline:
    def __init__(self, root: Operator, token: CancellationToken | None = None):
        self.root = root
        self.token = token or CancellationToken()
        root.bind_token(self.token)
        self._closed = False

    @property
    def columns(self):
        return self.root.columns

    def next_batch(self) -> Batch | None:
        return self.root.next_batch()

    # --- 驱动方式 ------------------------------------------------------
    def batches(self) -> Iterator[Batch]:
        """惰性批迭代器。

        正常走完、消费者中途 break、或迭代器被 GC——生成器关闭路径都会
        执行 finally，从而关闭整棵算子树。重复关闭安全。
        """
        try:
            while True:
                batch = self.root.next_batch()
                if batch is None:
                    break
                yield batch
        finally:
            self.close()

    def iter_rows(self) -> Iterator[Row]:
        for batch in self.batches():
            yield from batch.rows

    def run(self, consume: Callable[[Batch], None]) -> None:
        """逐批回调；回调里抛错也会关闭整棵树，再把异常原样抛出。"""
        for batch in self.batches():
            consume(batch)

    def drain(self) -> list[Row]:
        return list(self.iter_rows())

    def drain_batches(self) -> list[Batch]:
        return list(self.batches())

    def cancel(self, reason: str = "cancelled") -> None:
        self.token.cancel(reason)

    # --- 资源 ----------------------------------------------------------
    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        self.root.close()

    def __enter__(self) -> "Pipeline":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()
