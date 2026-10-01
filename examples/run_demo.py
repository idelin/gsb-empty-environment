"""运行样例：无偏洗牌、暂停/恢复、排名定位、可恢复外存洗牌、均匀性验证。"""
import json
import math
import sys
import tempfile
from collections import Counter
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from unbiased_shuffle import (
    CountingSource,
    ExternalShuffle,
    InjectedCrash,
    ShuffleState,
    unrank_partial,
    unrank_permutation,
)


def section(title):
    print(f"\n=== {title} ===")


def main():
    section("1. 注入随机源的无偏洗牌（记录 seed 与样本量）")
    src = CountingSource(seed=20261001)
    result = ShuffleState(list(range(10)), src).run()
    print("洗牌结果:", result)
    print("元数据  :", src.meta())

    section("2. 可暂停状态：中途检查点 -> 恢复 -> 与连续运行一致")
    src = CountingSource(seed=42)
    state = ShuffleState(list(range(8)), src)
    for _ in range(3):
        state.step()
    ckpt = state.checkpoint()
    print("暂停时检查点: frontier =", ckpt["frontier"],
          ", published =", ckpt["published"],
          ", seed =", ckpt["seed"], ", draws =", ckpt["draws"])
    restored = ShuffleState.restore(json.loads(json.dumps(ckpt)), CountingSource())
    print("恢复后继续  :", restored.run())
    continuous = ShuffleState(list(range(8)), CountingSource(seed=42)).run()
    print("连续运行    :", continuous, "-> 一致:", restored.items == continuous)

    section("3. 按排名定位排列（含大整数索引）")
    for rank in (0, 1, 60, 119):
        print(f"n=5, rank={rank:>3} ->", unrank_permutation(5, rank))
    big_rank = 2 ** 2500 + 987654321
    perm = unrank_permutation(1000, big_rank)
    print("n=1000, rank=2**2500+987654321 -> 前 5 项:", perm[:5])
    print("n=10**60 的 4-部分排列, rank=P(n,4)-1 ->",
          unrank_partial(10 ** 60, 4, math.perm(10 ** 60, 4) - 1))

    section("4. 可恢复外存洗牌：崩溃 -> 恢复 -> 与连续运行逐项一致")
    with tempfile.TemporaryDirectory() as tmp:
        n = 16
        ref_dir = Path(tmp) / "ref"
        ref_src = CountingSource(seed=777)
        ref = ExternalShuffle(ref_dir, sync=False)
        ref.initialize(list(range(n)), ref_src)
        ref_result = ref.run(ref_src)
        print("连续运行结果:", ref_result, "meta:", ref_src.meta())

        crash_dir = Path(tmp) / "crash"
        src = CountingSource(seed=777)
        shuffler = ExternalShuffle(crash_dir, sync=False)
        shuffler.initialize(list(range(n)), src)
        shuffler.fail_after = shuffler.op_count + 7  # 洗牌阶段第 7 个持久化操作前崩溃
        try:
            shuffler.run(src)
        except InjectedCrash as exc:
            print("注入崩溃    :", exc)
        ckpt = json.loads((crash_dir / "checkpoint.json").read_text())
        print("崩溃时检查点: frontier =", ckpt["frontier"],
              ", published =", ckpt["published"], ", draws =", ckpt["draws"])
        new_src = CountingSource()  # “进程重启”：全新随机源
        recovered = ExternalShuffle(crash_dir, sync=False)
        recovered.recover(new_src)
        result = recovered.run(new_src)
        print("恢复后结果  :", result, "meta:", new_src.meta())
        print("逐项一致    :", result == ref_result,
              "| 无重复无缺失:", sorted(result) == list(range(n)))

    section("5. 均匀性：精确结论 vs 经验估计")
    # 【精确结论】n=4 的全部 4! = 24 个随机分支与 24 个排列一一对应
    import itertools
    from tests.test_enumeration import shuffle_with_choices
    outcomes = Counter()
    for js in itertools.product(*[range(i + 1) for i in range(3, 0, -1)]):
        outcomes[shuffle_with_choices(4, js)] += 1
    print(f"[精确结论] n=4: {sum(outcomes.values())} 个分支 -> "
          f"{len(outcomes)} 个排列，每个恰出现 {set(outcomes.values())} 次")
    # 【经验估计】固定 seed 的频率统计
    src = CountingSource(seed=1)
    counts = Counter()
    trials = 24000
    for _ in range(trials):
        counts[tuple(ShuffleState([1, 2, 3], src).run())] += 1
    lo, hi = min(counts.values()), max(counts.values())
    print(f"[经验估计] n=3, {trials} 次试验: 每排列期望 {trials // 6} 次, "
          f"实测 [{lo}, {hi}]")


if __name__ == "__main__":
    main()
