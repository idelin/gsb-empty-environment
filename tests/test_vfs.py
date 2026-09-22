"""VFS 语义测试：路径不是身份、句柄绑定对象、错误类别区分。"""
import unittest

from common import small_spec
from trace_kernel.trace import ERR_NOT_FOUND, ERR_PERM
from trace_kernel.vfs import FSError


class TestVFS(unittest.TestCase):
    def test_handle_survives_rename_and_unlink(self):
        vfs = small_spec()
        h = vfs.open("/a/f")
        vfs.rename("/a/f", "/a/f2")
        self.assertEqual(vfs.read(h), "f1")     # 改名后句柄仍指原对象
        vfs.unlink("/a/f2")
        self.assertEqual(vfs.read(h), "f1")     # 删除路径后句柄仍有效

    def test_hardlink_shares_object(self):
        vfs = small_spec()
        hf = vfs.open("/a/f")
        hl = vfs.open("/a/l")
        self.assertEqual(vfs.handles[hf], vfs.handles[hl])
        vfs.write("/a/l", "shared")
        self.assertEqual(vfs.read(hf), "shared")

    def test_open_binds_version(self):
        vfs = small_spec()
        h = vfs.open("/a/f")
        rec = vfs.trace.records[-1]
        self.assertEqual((rec["obj"], rec["version"]),
                         (vfs.handles[h], 1))

    def test_perm_vs_not_found_distinct(self):
        vfs = small_spec()
        with self.assertRaises(FSError) as c1:
            vfs.open("/c/secret")
        with self.assertRaises(FSError) as c2:
            vfs.lookup("/a/missing")
        self.assertEqual(c1.exception.kind, ERR_PERM)
        self.assertEqual(c2.exception.kind, ERR_NOT_FOUND)
        # 失败查询记录实际解析过的父目录与缺失名
        rec = vfs.trace.records[-1]
        self.assertEqual(rec["resolution"][-1]["name"], "missing")
        self.assertEqual(rec["resolution"][-1]["error"], ERR_NOT_FOUND)

    def test_symlink_resolution_recorded(self):
        vfs = small_spec()
        vfs.open("/a/s")
        rec = vfs.trace.records[-1]
        hops = [s for s in rec["resolution"] if "symlink" in s]
        self.assertEqual(len(hops), 1)
        self.assertEqual(hops[0]["target"], "/a/f")

    def test_enum_prefix_vs_full(self):
        vfs = small_spec()
        vfs.enum("/a", limit=1)
        self.assertEqual(vfs.trace.records[-1]["mode"], "prefix")
        vfs.enum("/a")
        self.assertEqual(vfs.trace.records[-1]["mode"], "full")
        vfs.enum("/a", limit=99)   # 限额 >= 成员数 => 实际看到全部
        self.assertEqual(vfs.trace.records[-1]["mode"], "full")


if __name__ == "__main__":
    unittest.main()
