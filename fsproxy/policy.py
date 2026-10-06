"""Readable-input / writable-output subtree policy."""

from .errors import EscapeError


def normalize_subtree(path):
    """Normalize a policy subtree to a root-relative path ("" == whole root)."""
    if path.startswith("/"):
        raise ValueError(f"policy subtree must be relative: {path!r}")
    parts = [c for c in path.split("/") if c not in ("", ".")]
    if any(c == ".." for c in parts):
        raise ValueError(f"policy subtree must not contain '..': {path!r}")
    return "/".join(parts)


def _under(path, subtree):
    return subtree == "" or path == subtree or path.startswith(subtree + "/")


class Policy:
    """A set of readable input subtrees and writable output subtrees.

    Checks always run against the *resolved* root-relative path (symlinks
    spliced, ``..`` folded), never against the caller-supplied spelling.
    """

    def __init__(self, readable=(), writable=()):
        self.readable = tuple(normalize_subtree(p) for p in readable)
        self.writable = tuple(normalize_subtree(p) for p in writable)

    def can_read(self, resolved_path):
        return any(_under(resolved_path, sub) for sub in self.readable)

    def can_write(self, resolved_path):
        return any(_under(resolved_path, sub) for sub in self.writable)


def split_components(path):
    """Split a caller path into components; reject absolute paths."""
    if path.startswith("/"):
        raise EscapeError(f"absolute paths are not allowed: {path!r}")
    return [c for c in path.split("/") if c not in ("", ".")]
