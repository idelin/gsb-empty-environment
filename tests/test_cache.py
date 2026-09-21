import tempfile
import unittest
from pathlib import Path

from buildset.cache import BuildCache
from buildset.resolver import BuildNode


def node(name, version, kind, partition, os_name, arch):
    return BuildNode(
        name=name,
        version=version,
        kind=kind,
        partition=partition,
        cache_key=(name, version, os_name, arch),
    )


class CacheTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cache = BuildCache(self.tmp.name)

    def test_host_and_target_partitions_are_separate(self):
        tool = node("gen", "3.1.0", "tool", "host", "linux", "x86_64")
        lib = node("lib", "2.0.0", "library", "target", "darwin", "arm64")
        tool_path = self.cache.store(tool)
        lib_path = self.cache.store(lib)
        self.assertEqual(tool_path.relative_to(self.tmp.name).parts[:2],
                         ("host", "linux-x86_64"))
        self.assertEqual(lib_path.relative_to(self.tmp.name).parts[:2],
                         ("target", "darwin-arm64"))

    def test_lookup_hit_and_miss(self):
        lib = node("lib", "2.0.0", "library", "target", "darwin", "arm64")
        self.assertIsNone(self.cache.lookup(lib))
        self.cache.store(lib)
        self.assertIsNotNone(self.cache.lookup(lib))

    def test_host_tool_shared_between_targets(self):
        tool = node("gen", "3.1.0", "tool", "host", "linux", "x86_64")
        self.cache.store(tool)
        # Same host tool key regardless of which target requested the build.
        self.assertIsNotNone(self.cache.lookup(tool))
        hits, misses = self.cache.plan([tool])
        self.assertEqual(hits, [tool])
        self.assertEqual(misses, [])

    def test_plan_splits_hits_and_misses(self):
        cached = node("a", "1.0.0", "library", "target", "darwin", "arm64")
        fresh = node("b", "1.0.0", "library", "target", "darwin", "arm64")
        self.cache.store(cached)
        hits, misses = self.cache.plan([cached, fresh])
        self.assertEqual(hits, [cached])
        self.assertEqual(misses, [fresh])


if __name__ == "__main__":
    unittest.main()
