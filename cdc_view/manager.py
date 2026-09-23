"""视图管理器：后台重建、增量缓存、追平校验、原子切换。

流程：
  1. start_rebuild 拿到固定快照（表数据 + 快照水位），后台线程构建新视图；
  2. 构建期间到达的变更在 apply_change 中进入缓冲区（同时仍应用到旧视图，
     保证旧读者看到的数据持续新鲜）；
  3. 构建完成后，按 commit_seq 升序回放缓冲区中水位之后的增量（追平）；
  4. 仅当新视图水位 >= 当前视图水位时才原子切换，否则保持 BUILDING 不发布；
  5. 构建失败时保留旧视图对外服务，状态记为 FAILED。
"""

from __future__ import annotations

import threading
import time
from dataclasses import dataclass
from typing import Callable, Optional

from .events import ChangeEvent
from .view import MaterializedView


class RebuildError(RuntimeError):
    pass


@dataclass
class _RebuildState:
    snapshot_watermark: int
    pending: Optional[MaterializedView] = None
    status: str = "BUILDING"  # BUILDING -> PUBLISHED | FAILED
    error: Optional[str] = None


class ViewManager:
    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._current: Optional[MaterializedView] = None
        self._buffer: list[ChangeEvent] = []
        self._rebuild: Optional[_RebuildState] = None
        self._version = 0
        self.last_rebuild_status: Optional[str] = None
        self.last_rebuild_error: Optional[str] = None

    # ---- 引导与读取 ----

    def bootstrap(
        self, tables: dict[str, list[dict]], watermark: int
    ) -> MaterializedView:
        """用初始快照发布 v1。"""
        with self._lock:
            self._version += 1
            self._current = MaterializedView.from_snapshot(
                self._version, tables, watermark
            )
            return self._current

    def acquire(self) -> MaterializedView:
        """读者获取当前版本句柄；切换后旧句柄仍按旧版本可读。"""
        with self._lock:
            if self._current is None:
                raise RebuildError("no view published yet")
            return self._current

    def current_version(self) -> int:
        with self._lock:
            return self._version

    @property
    def rebuild_in_progress(self) -> bool:
        with self._lock:
            return self._rebuild is not None

    # ---- 变更入口 ----

    def apply_change(self, event: ChangeEvent) -> None:
        """应用一条变更：重建期间缓存增量，同时保持旧视图新鲜。"""
        with self._lock:
            if self._rebuild is not None:
                self._buffer.append(event)
            if self._current is not None:
                self._current.apply(event)
            self._drain_and_maybe_publish_locked()

    # ---- 重建 ----

    def start_rebuild(
        self,
        snapshot_tables: dict[str, list[dict]],
        snapshot_watermark: int,
        *,
        build_hook: Optional[Callable[[], None]] = None,
        build_delay: float = 0.0,
        fail: bool = False,
    ) -> threading.Thread:
        """从固定快照后台重建。build_hook/fail 供测试与演示注入。"""
        with self._lock:
            if self._rebuild is not None:
                raise RebuildError("rebuild already in progress")
            state = _RebuildState(snapshot_watermark=snapshot_watermark)
            self._rebuild = state
            self._buffer = []
        thread = threading.Thread(
            target=self._build_worker,
            args=(state, snapshot_tables, build_hook, build_delay, fail),
            name="view-rebuilder",
            daemon=True,
        )
        thread.start()
        return thread

    def cancel_rebuild(self) -> None:
        """放弃当前重建（例如快照过旧永远追不平），旧视图不受影响。"""
        with self._lock:
            self._rebuild = None
            self._buffer.clear()
            self.last_rebuild_status = "CANCELLED"

    def _build_worker(
        self,
        state: _RebuildState,
        snapshot_tables: dict[str, list[dict]],
        build_hook: Optional[Callable[[], None]],
        build_delay: float,
        fail: bool,
    ) -> None:
        try:
            if fail:
                raise RebuildError("injected build failure")
            if build_delay > 0:
                time.sleep(build_delay)  # 模拟全量构建耗时
            new_view = MaterializedView.from_snapshot(
                0, snapshot_tables, state.snapshot_watermark
            )
            if build_hook is not None:
                build_hook()  # 构建完成、尚未进入追平（测试可在此注入迟到事件）
        except Exception as exc:  # 失败：保留旧视图可用
            with self._lock:
                state.status = "FAILED"
                state.error = str(exc)
                self.last_rebuild_status = "FAILED"
                self.last_rebuild_error = str(exc)
                self._buffer.clear()
                self._rebuild = None
            return
        with self._lock:
            state.pending = new_view
            self._drain_and_maybe_publish_locked()

    def _drain_and_maybe_publish_locked(self) -> None:
        """追平并尝试发布；未追平则保持 BUILDING，绝不发布。"""
        state = self._rebuild
        if state is None or state.pending is None or self._current is None:
            return
        # 追平：只回放快照水位之后的增量，按提交序号升序
        backlog = sorted(
            (e for e in self._buffer if e.commit_seq > state.snapshot_watermark),
            key=lambda e: e.commit_seq,
        )
        for event in backlog:
            state.pending.apply(event)
        if state.pending.watermark < self._current.watermark:
            return  # 未追平（快照过旧，缺失的增量不在缓冲区），不得发布
        # 原子切换：冻结旧视图，指针换新；旧读者句柄仍指向旧版本
        self._current.freeze()
        self._version += 1
        state.pending.version = self._version
        self._current = state.pending
        self._buffer.clear()
        state.status = "PUBLISHED"
        self.last_rebuild_status = "PUBLISHED"
        self.last_rebuild_error = None
        self._rebuild = None
