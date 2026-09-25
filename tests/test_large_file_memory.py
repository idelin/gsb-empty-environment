import shutil
import tempfile
import tracemalloc
import unittest

from evidence.exporter import export_package
from evidence.manifest import FileEntry
from evidence.receiver import Chunk, Receiver
from evidence.sample import synth_bytes
from evidence.signoff import sign_off
from evidence.store import Store
from evidence.validator import validate_manifest
from evidence.verifier import verify_package


FILE_ID = "bulk-synthetic.bin"
FILE_SIZE = 16 * 1024 * 1024
CHUNK_SIZE = 64 * 1024
PEAK_BUDGET = 2 * 1024 * 1024  # 2 MiB resident Python memory


class LargeFileMemoryTest(unittest.TestCase):
    def test_big_file_is_hashed_and_exported_chunk_wise(self):
        store = Store(":memory:")
        receiver = Receiver(store)
        data = synth_bytes(FILE_SIZE, seed=b"bulk")
        entry = FileEntry.from_bytes(FILE_ID, data, CHUNK_SIZE)
        from evidence.manifest import Manifest
        manifest = Manifest(manifest_id="bulk-case", version=1,
                            files=(entry,), references=())
        receiver.receive_manifest(manifest)

        def stream_chunks():
            for index in range(entry.chunk_count):
                start = index * CHUNK_SIZE
                yield Chunk(manifest.manifest_id, 1, FILE_ID, index,
                            data[start:start + CHUNK_SIZE])

        for chunk in stream_chunks():
            receiver.receive_chunk(chunk)
        del data

        dest = tempfile.mkdtemp(prefix="evidence-bulk-")
        self.addCleanup(shutil.rmtree, dest, ignore_errors=True)

        tracemalloc.start()
        report = validate_manifest(store, manifest)
        self.assertTrue(report.ok, [e.code for e in report.errors])
        result = sign_off(store, "bulk-case")
        self.assertTrue(result.ok)
        export_package(store, "bulk-case", dest)
        export_peak = tracemalloc.get_traced_memory()[1]
        tracemalloc.stop()

        tracemalloc.start()
        verification = verify_package(dest)
        verify_peak = tracemalloc.get_traced_memory()[1]
        tracemalloc.stop()

        self.assertTrue(verification.ok,
                        [f.code for f in verification.failures])
        self.assertLess(
            export_peak, PEAK_BUDGET,
            f"export/validate peak {export_peak} exceeded chunk budget")
        self.assertLess(
            verify_peak, PEAK_BUDGET,
            f"verify peak {verify_peak} exceeded chunk budget")


if __name__ == "__main__":
    unittest.main()
