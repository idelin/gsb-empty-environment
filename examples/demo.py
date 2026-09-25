#!/usr/bin/env python3
"""可运行样例:多轨静音检测、片段切分、修订补丁与 checkpoint 恢复。

运行: python3.11 examples/demo.py
"""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from silence_split import fixtures
from silence_split.core import window_rms
from silence_split.session import Session
from silence_split.streaming import StreamingDetector

W = 80


def show_segments(title, sess):
    print(f"\n== {title} ==")
    for s in sess.segments:
        print(f"  {s.id}  导出区间 [{s.start:5d}, {s.end:5d})  "
              f"核心 [{s.core_start:5d}, {s.core_end:5d})")


def show_events(title, events):
    print(f"\n-- {title} --")
    for e in events:
        print(" ", e)


def main():
    cfg = fixtures.base_config()
    print(f"配置: 窗口 {cfg.window_size} 采样, min_silence {cfg.min_silence}, "
          f"min_sound {cfg.min_sound}, pre/post 保留 {cfg.pre_pad}/{cfg.post_pad}")
    print(f"声明: 确认延迟 <= {cfg.confirmation_latency_samples} 采样, "
          f"导出延迟 <= {cfg.export_latency_samples} 采样, "
          f"数值容差 {cfg.tolerance}")

    # 1) 初始分段:tone30 + silence10 + tone30 + silence10(窗口)
    sess = Session(cfg)
    sess.write("main", 0, fixtures.concat(
        fixtures.tone(30 * W), fixtures.silence(10 * W),
        fixtures.tone(30 * W), fixtures.silence(10 * W)))
    sess.poll()
    show_segments("初始分段(两段有声,各由其后静音确认)", sess)
    sess.export_all()
    print("已导出:", sorted(sess.exporter.records))

    # 2) 补音:两段之间的静音改写为有声 → 合并补丁
    events = sess.write("main", 30 * W, fixtures.tone(10 * W), overwrite=True)
    show_events("补音(静音→有声)→ 合并补丁", events)
    show_segments("合并后", sess)

    # 3) 撤回补音(置零)→ 拆分补丁,原 ID 可追溯恢复
    events = sess.retract(30 * W, 40 * W)
    show_events("撤回补音(置零)→ 拆分补丁", events)
    show_segments("拆分后(稳定 ID 可追溯)", sess)

    print("\n导出登记处历史(修订事件全程可追溯):")
    for row in sess.exporter.history:
        print(" ", row)

    # 4) checkpoint 在候选静音期间保存并恢复,结果对照全量参考
    rms = [window_rms(fixtures.tone(W)) for _ in range(30)] + \
          [window_rms(fixtures.silence(W)) for _ in range(10)] + \
          [window_rms(fixtures.tone(W)) for _ in range(30)]
    det = StreamingDetector(cfg)
    for r in rms[:32]:  # 候选静音第 2/4 窗口处
        det.push_window([r])
    blob = json.dumps(det.checkpoint())
    restored = StreamingDetector.restore(cfg, 1, json.loads(blob))
    for r in rms[32:]:
        det.push_window([r])
        restored.push_window([r])
    print(f"\ncheckpoint 于候选静音期间(32/{len(rms)} 窗口)恢复:")
    print(f"  恢复后最终核心区间 {restored.finish()}  "
          f"与全量参考一致: {restored.finish() == det.finish()}")


if __name__ == "__main__":
    main()
