"""SHA-256 addressed object storage for immutable blobs.

Objects live at ``objects/xx/yyyy...`` (first two digest hex digits form the
shard directory).  Writing is staged in a unique temporary file; once the
full stream has been read and its declared length and digest verified, the
temporary file is fsynced and ``os.replace``d onto its final path.  Two
imports of identical bytes therefore share one object, and a reader can only
ever observe a complete object.
"""

import hashlib
import os
import tempfile


def digest_hex(raw):
    return hashlib.sha256(raw).hexdigest()


def object_relpath(digest):
    digest = digest.lower()
    return os.path.join("objects", digest[:2], digest[2:])


class ContentStore:
    """A directory of immutable, content-addressed blobs."""

    def __init__(self, root):
        self.root = root
        self.objects = os.path.join(root, "objects")
        os.makedirs(self.objects, exist_ok=True)

    def path(self, digest):
        return os.path.join(self.root, object_relpath(digest))

    def exists(self, digest):
        return os.path.isfile(self.path(digest))

    def has_all(self, digests):
        return all(self.exists(d) for d in digests)

    def all_digests(self):
        result = set()
        if not os.path.isdir(self.objects):
            return result
        for shard in os.listdir(self.objects):
            shard_path = os.path.join(self.objects, shard)
            if not os.path.isdir(shard_path):
                continue
            for tail in os.listdir(shard_path):
                if len(shard) == 2 and len(tail) == 62:
                    result.add((shard + tail).lower())
        return result

    def commit(self, tmp_path, digest):
        """Publish a staged object; identical content is shared, never copied."""
        final = self.path(digest)
        if os.path.exists(final):
            _unlink(tmp_path)
            return False
        os.makedirs(os.path.dirname(final), exist_ok=True)
        os.replace(tmp_path, final)
        _fsync_dir(os.path.dirname(final))
        return True

    def abandon(self, tmp_path):
        _unlink(tmp_path)

    def delete(self, digest):
        path = self.path(digest)
        try:
            os.unlink(path)
        except FileNotFoundError:
            return False
        return True

    def open_blob(self, digest):
        return open(self.path(digest), "rb")

    def verify(self, digest):
        """Return True if the stored object still hashes to ``digest``."""
        path = self.path(digest)
        if not os.path.isfile(path):
            return False
        hasher = hashlib.sha256()
        with open(path, "rb") as handle:
            for chunk in iter(lambda: handle.read(1 << 16), b""):
                hasher.update(chunk)
        return hasher.hexdigest() == digest.lower()


def _unlink(path):
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass


def _fsync_dir(path):
    try:
        fd = os.open(path, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)
