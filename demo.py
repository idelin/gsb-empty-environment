"""运行样例：采样率切换 + 缺口 + 版本重叠 + 校准更新与稳定游标。"""
import fixtures
from timeline import Block, Timeline


def show_read(title, res):
    print(f"-- {title} [{res.t0}, {res.t1})")
    for s in res.segments:
        print(f"   段 {s.block_id} v{s.version} 样本[{s.sample_start}:{s.sample_end}]"
              f" 时间[{s.t_start:.6f}, {s.t_end:.6f}) 样本数={len(s.samples)}")
    for h in res.holes:
        print(f"   空洞 [{h.t_start:.6f}, {h.t_end:.6f})")
    if not res.segments and not res.holes:
        print("   （空）")


def main():
    print(f"声明延迟={fixtures.DECLARED_LATENCY_S}s "
          f"数值容差={fixtures.TIME_TOLERANCE}s（样本来自本地夹具，无音频设备）")

    tl = Timeline()
    # 采样率切换：a8k [0, 0.1) @8kHz，b16k [0.1, 0.2) @16kHz
    for b in fixtures.rate_switch_blocks():
        tl.add_block(b)
    # 缺口：tail [0.225, 0.325)，与 b16k 之间留 25ms 空洞
    tl.add_block(Block("tail", 8000.0, fixtures.ramp(800, start=5.0),
                       nominal_start=0.225, version=1,
                       declared_latency=fixtures.DECLARED_LATENCY_S))
    # 重叠：patch v2 [0.05, 0.15)，遮蔽 a8k 尾部与 b16k 头部
    tl.add_block(Block("patch", 8000.0, fixtures.ramp(800, start=9.0),
                       nominal_start=0.05, version=2,
                       declared_latency=fixtures.DECLARED_LATENCY_S))

    print("\n== 不相交切片（版本裁决后） ==")
    for s in tl.slices:
        print(f"   {s.block_id} v{s.version} 样本[{s.sample_start}:{s.sample_end}]"
              f" 时间[{s.t_start:.6f}, {s.t_end:.6f})"
              f" 舍入证据 raw=({s.raw_start_pos:.6f}, {s.raw_end_pos:.6f})")
    print("== 全局空洞 ==")
    for h in tl.holes:
        print(f"   [{h.t_start:.6f}, {h.t_end:.6f})")

    print("\n== 按时间读取 ==")
    show_read("跨采样率切换", tl.read(0.04, 0.19))
    show_read("跨缺口", tl.read(0.19, 0.24))

    print("\n== 双向查询 ==")
    print("   样本范围 b16k[400:800] -> 时间",
          tuple(round(t, 6) for t in tl.sample_to_time("b16k", 400, 800)))
    ev = tl.time_to_sample("b16k", 0.137)
    print(f"   时间 0.137 -> b16k 下标 {ev.index}"
          f"（小数位置 {ev.position:.6f}，余数 {ev.residue:.6f}）")

    print("\n== 校准更新：b16k 偏移 +0.05s ==")
    cursor = tl.cursor()  # 旧游标
    old = tl.sample_to_time("b16k", 0)
    tl.set_calibration("b16k", 0.05)
    new = tl.sample_to_time("b16k", 0)
    print(f"   旧映射 b16k[0] -> {old[0]:.6f}；新映射 -> {new[0]:.6f}")
    print(f"   旧游标仍读到   -> {cursor.sample_to_time('b16k', 0)[0]:.6f}（稳定）")
    show_read("校准后读取", tl.read(0.1, 0.2))
    show_read("旧游标读取同一区间", cursor.read(0.1, 0.2))


if __name__ == "__main__":
    main()
