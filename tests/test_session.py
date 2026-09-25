"""会话级:缺口与真实静音、补音合并/撤回拆分、标记修订、稳定 ID、
已导出片段的修订事件、多轨策略、整段与分块对照。"""
import unittest

from silence_split import fixtures
from silence_split.events import (SegmentCreated, SegmentDiscarded,
                                  SegmentMerged, SegmentRevised, SegmentSplit)
from silence_split.session import Session

W = 80


def make_session(**overrides):
    return Session(fixtures.base_config(**overrides))


def tone_windows(n):
    return fixtures.tone(n * W)


def silence_windows(n):
    return fixtures.silence(n * W)


def two_segment_session():
    """tone30 + silence10 + tone30 + silence10:两个已确认片段。"""
    sess = make_session()
    sess.write("main", 0, fixtures.concat(
        tone_windows(30), silence_windows(10),
        tone_windows(30), silence_windows(10)))
    sess.poll()
    assert len(sess.segments) == 2
    return sess


class TestGapVsSilence(unittest.TestCase):
    def test_gap_stalls_then_true_silence_matches_reference(self):
        sess = make_session()
        sess.write("main", 0, tone_windows(30))
        sess.write("main", 40 * W, tone_windows(30))  # [2400,3200) 为缺口
        sess.poll()
        self.assertEqual(sess.segments, [])   # 缺口阻塞,无确认片段
        self.assertTrue(sess.stalled)
        # 缺口填入真实静音(零)后推进
        events = sess.write("main", 30 * W, silence_windows(10))
        self.assertFalse(sess.stalled)
        self.assertEqual([e.kind for e in events], ["created"])
        sess.finish()
        self.assertEqual(len(sess.segments), 2)
        # 对照全量参考(一次性写入相同内容)
        ref = make_session()
        ref.write("main", 0, fixtures.concat(
            tone_windows(30), silence_windows(10), tone_windows(30)))
        ref.finish()
        self.assertEqual(sess.segments, ref.segments)

    def test_gap_filled_with_sound_merges(self):
        sess = make_session()
        sess.write("main", 0, tone_windows(30))
        sess.write("main", 40 * W, tone_windows(30))
        sess.poll()
        sess.write("main", 30 * W, tone_windows(10))  # 补音填平缺口
        sess.poll()
        self.assertEqual(sess.segments, [])  # 全程有声,无确认切分
        sess.finish()
        self.assertEqual(len(sess.segments), 1)
        self.assertEqual(sess.segments[0].core_start, 0)
        self.assertEqual(sess.segments[0].core_end, 70 * W)

    def test_erase_returns_to_gap_and_discards(self):
        sess = make_session()
        sess.write("main", 0, tone_windows(30))
        sess.write("main", 40 * W,
                   fixtures.concat(tone_windows(30), silence_windows(10)))
        sess.poll()
        self.assertEqual(sess.segments, [])
        sess.write("main", 30 * W, tone_windows(10))  # 补音 → 合并为一段
        sess.poll()
        self.assertEqual(len(sess.segments), 1)
        events = sess.erase(30 * W, 40 * W)  # 撤回补音为缺口 → 片段废弃
        self.assertEqual(sess.segments, [])
        self.assertEqual([e.kind for e in events], ["discarded"])
        self.assertIsInstance(events[0], SegmentDiscarded)


class TestRevisionPatches(unittest.TestCase):
    def test_backfill_merges_then_retract_splits(self):
        sess = two_segment_session()
        s1, s2 = sess.segments
        self.assertEqual(s1.id, "seg-00000000")
        self.assertEqual(s2.id, "seg-00000c80")
        sess.export_all()
        # 补音:把两段之间的静音改写为有声 → 合并
        events = sess.write("main", 30 * W, tone_windows(10), overwrite=True)
        self.assertEqual(len(events), 1)
        merge = events[0]
        self.assertIsInstance(merge, SegmentMerged)
        self.assertEqual(merge.surviving_id, s1.id)       # 最前段 ID 存活
        self.assertEqual(merge.absorbed_ids, (s2.id,))
        self.assertEqual(len(sess.segments), 1)
        # 撤回补音(置零)→ 重新拆分,且原 ID 可追溯地恢复
        events = sess.retract(30 * W, 40 * W)
        self.assertEqual(len(events), 1)
        split = events[0]
        self.assertIsInstance(split, SegmentSplit)
        self.assertEqual(split.source_id, s1.id)
        self.assertEqual(split.created_ids, (s2.id,))
        self.assertEqual([s.id for s in sess.segments], [s1.id, s2.id])
        # 导出登记处全程可追溯
        self.assertEqual(sess.exporter.records[s1.id]["version"], 3)
        self.assertEqual(sess.exporter.records[s2.id]["status"], "active")
        self.assertEqual(sess.exporter.records[s2.id]["version"], 2)

    def test_exported_segment_revised_on_boundary_change(self):
        sess = two_segment_session()
        sess.export_all()
        # 第一段有声延长 2 窗(静音缩短为 8 窗,仍 >= 4)→ 边界修订
        events = sess.write("main", 30 * W, tone_windows(2), overwrite=True)
        self.assertEqual(len(events), 1)
        rev = events[0]
        self.assertIsInstance(rev, SegmentRevised)
        self.assertEqual(rev.segment_id, "seg-00000000")
        self.assertEqual(rev.old_end, 31 * W)
        self.assertEqual(rev.new_end, 33 * W)
        self.assertEqual(rev.version, 2)  # 已导出 → 版本递增
        # 第二段完全不受影响
        self.assertEqual(sess.segments[1].id, "seg-00000c80")
        self.assertEqual(sess.exporter.records["seg-00000c80"]["version"], 1)

    def test_unaffected_segment_keeps_id_and_emits_nothing(self):
        sess = two_segment_session()
        before = list(sess.segments)
        # 只改动第二段之后的静音(缩短末尾静音,仍 >= 4)
        events = sess.write("main", 70 * W, tone_windows(2), overwrite=True)
        self.assertEqual(sess.segments[0], before[0])
        touched = {getattr(e, "segment_id", None) for e in events}
        touched |= {getattr(e, "surviving_id", None) for e in events}
        touched |= {getattr(e, "source_id", None) for e in events}
        self.assertNotIn(before[0].id, touched)


class TestMarkers(unittest.TestCase):
    def test_marker_revision_triggers_local_resegment(self):
        sess = make_session()
        sess.write("main", 0, fixtures.concat(tone_windows(60),
                                              silence_windows(10)))
        sess.poll()
        self.assertEqual(len(sess.segments), 1)
        # 修订静音标记:中段强制静音 → 局部重分段 → 拆分
        events = sess.add_marker(20 * W, 30 * W, "force_silence")
        self.assertEqual([e.kind for e in events], ["split"])
        self.assertEqual(len(sess.segments), 2)
        self.assertEqual(sess.segments[1].id, "seg-00000960")  # 30*80
        # 撤销标记 → 合并回原段
        events = sess.clear_markers()
        self.assertEqual([e.kind for e in events], ["merged"])
        self.assertEqual(len(sess.segments), 1)
        self.assertEqual(sess.segments[0].id, "seg-00000000")


class TestMultiTrack(unittest.TestCase):
    def test_any_strategy(self):
        sess = Session(fixtures.base_config(strategy="any"), tracks=("a", "b"))
        sess.write("a", 0, fixtures.concat(tone_windows(30), silence_windows(30)))
        sess.write("b", 0, fixtures.concat(silence_windows(30), tone_windows(30)))
        sess.finish()
        self.assertEqual(len(sess.segments), 1)  # 任一轨有声 → 全程有声

    def test_all_strategy(self):
        sess = Session(fixtures.base_config(strategy="all"), tracks=("a", "b"))
        sess.write("a", 0, fixtures.concat(tone_windows(40), silence_windows(20)))
        sess.write("b", 0, fixtures.concat(silence_windows(20), tone_windows(40)))
        sess.finish()
        # 仅窗口 [20,40) 两轨同时有声
        self.assertEqual(len(sess.segments), 1)
        self.assertEqual(sess.segments[0].core_start, 20 * W)
        self.assertEqual(sess.segments[0].core_end, 40 * W)


class TestChunkedWrites(unittest.TestCase):
    def test_chunked_writes_equal_whole_reference(self):
        full = fixtures.concat(tone_windows(30), silence_windows(10),
                               tone_windows(30), silence_windows(10))
        ref = make_session()
        ref.write("main", 0, full)
        ref.poll()
        chunked = make_session()
        pos = 0
        for n in [137, 80, 1000, 1, 333, 2049]:
            chunk = full[pos:pos + n]
            chunked.write("main", pos, chunk)
            chunked.poll()
            pos += len(chunk)
        chunked.write("main", pos, full[pos:])
        chunked.poll()
        self.assertEqual(chunked.segments, ref.segments)
        self.assertEqual(len(ref.segments), 2)


if __name__ == "__main__":
    unittest.main()
