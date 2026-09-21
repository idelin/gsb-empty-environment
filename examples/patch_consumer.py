"""补丁消费示例：下游只维护一份物化视图，靠 SegmentPatch 增量同步。

运行方式（仓库根目录）：
    python3 examples/patch_consumer.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from rle_history import Segment, SegmentPatch, StateHistory, apply_patch


class MaterializedView:
    """补丁消费方：不接触原始事件，只按补丁维护段列表。"""

    def __init__(self, initial: list[Segment]) -> None:
        self._segments = list(initial)

    def consume(self, patch: SegmentPatch) -> None:
        self._segments = apply_patch(self._segments, patch)

    def state_at(self, time: int):
        for seg in self._segments:
            if seg.covers(time):
                return seg.state
        return None

    def dump(self) -> None:
        for seg in self._segments:
            end = "+inf" if seg.end is None else seg.end
            sources = ", ".join(str(r) for r in seg.sources)
            extra = ""
            if seg.overridden:
                extra = "  overridden: " + ", ".join(str(r) for r in seg.overridden)
            print(f"    [{seg.start}, {end})  {seg.state}  sources: {sources}{extra}")
        if not self._segments:
            print("    <empty>")


def show(patch: SegmentPatch) -> None:
    for seg in patch.removed:
        end = "+inf" if seg.end is None else seg.end
        print(f"    - [{seg.start}, {end}) {seg.state}")
    for seg in patch.added:
        end = "+inf" if seg.end is None else seg.end
        print(f"    + [{seg.start}, {end}) {seg.state}")


def main() -> None:
    history = StateHistory()
    view = MaterializedView([])

    def edit(desc: str, fn) -> None:
        patch = fn()
        print(f"{desc}")
        show(patch)
        view.consume(patch)  # 消费补丁
        assert view._segments == history.segments(), "视图与主库发散！"

    edit("insert A@0", lambda: history.insert(0, "A", "dev", 1))
    edit("insert A@2", lambda: history.insert(2, "A", "dev", 2))
    edit("insert B@5", lambda: history.insert(5, "B", "dev", 3))
    edit("insert B@1 (拆开 A 段)", lambda: history.insert(1, "B", "dev", 4))
    edit("retract dev#4 (撤回 B@1，恢复原结构)", lambda: history.retract("dev", 4))
    edit("correct dev#3 state -> C", lambda: history.correct("dev", 3, state="C"))

    print("\n最终物化视图：")
    view.dump()
    print("\n抽查 state_at：")
    for t in (-1, 0, 1, 4, 5, 100):
        print(f"    t={t}: {view.state_at(t)}")
    print("\n视图与主库一致，补丁消费完成。")


if __name__ == "__main__":
    main()
