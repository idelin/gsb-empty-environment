import unittest

from imagerepo.closure import (
    CONTENT_MISSING,
    DIGEST_MISMATCH,
    METADATA_MISSING,
    compute_closure,
)
from imagerepo.model import HOST_PLATFORM, Dep, Ident, Metadata


class ClosureTests(unittest.TestCase):
    def setUp(self):
        self.target = "linux-x86_64"
        self.libc = Ident("libc", "1", self.target)
        self.gcc = Ident("gcc", "1", HOST_PLATFORM)
        self.app = Ident("app", "1", self.target)
        self.metadb = {
            self.libc: Metadata(self.libc, "a" * 64, 3, (), ()),
            self.gcc: Metadata(self.gcc, "b" * 64, 4, (), ()),
            self.app: Metadata(
                self.app, "c" * 64, 5,
                (Dep("libc", "1"),), (Dep("gcc", "1"),)),
        }
        self.objects = {"a" * 64: 3, "b" * 64: 4, "c" * 64: 5}

    def get_meta(self, ident):
        return self.metadb.get(ident)

    def has_object(self, digest, length):
        return self.objects.get(digest) == length

    def test_both_closures_complete(self):
        result = compute_closure([self.app], self.get_meta, self.has_object)
        self.assertTrue(result.ok)
        self.assertEqual(result.runtime, {self.app, self.libc})
        self.assertEqual(result.build, {self.app, self.libc, self.gcc})

    def test_metadata_missing_with_root_path(self):
        del self.metadb[self.gcc]
        result = compute_closure([self.app], self.get_meta, self.has_object)
        self.assertFalse(result.ok)
        gap = result.gaps[0]
        self.assertEqual(gap.kind, METADATA_MISSING)
        self.assertEqual(gap.ident, self.gcc)
        self.assertEqual(gap.path, (self.app, self.gcc))

    def test_content_missing_with_path(self):
        del self.objects["a" * 64]
        result = compute_closure([self.app], self.get_meta, self.has_object)
        kinds = {g.kind for g in result.gaps}
        self.assertIn(CONTENT_MISSING, kinds)
        gap = next(g for g in result.gaps if g.ident == self.libc)
        self.assertEqual(gap.path, (self.app, self.libc))

    def test_digest_mismatch(self):
        def bad_check(meta):
            return "sha-256 mismatch" if meta.ident == self.libc else None
        result = compute_closure([self.app], self.get_meta, self.has_object,
                                 check_digest=bad_check)
        self.assertEqual(result.gaps[0].kind, DIGEST_MISMATCH)
        self.assertEqual(result.gaps[0].ident, self.libc)

    def test_host_tools_keep_host_platform(self):
        binutils = Ident("binutils", "1", HOST_PLATFORM)
        self.metadb[self.gcc] = Metadata(
            self.gcc, "b" * 64, 4, (Dep("binutils", "1"),), ())
        self.metadb[binutils] = Metadata(binutils, "d" * 64, 6, (), ())
        self.objects["d" * 64] = 6
        result = compute_closure([self.app], self.get_meta, self.has_object)
        self.assertTrue(result.ok, result.gaps)
        self.assertIn(binutils, result.build)
        self.assertNotIn(binutils, result.runtime)


if __name__ == "__main__":
    unittest.main()
