"""Atomic no-clobber rename (renameat2/RENAME_NOREPLACE on Linux).

Linux is the supported target.  A Darwin fallback (renameatx_np/RENAME_EXCL)
exists only so the test-suite can be developed on macOS; it provides the
same "fail instead of clobbering a concurrently created target" semantics.
"""

from __future__ import annotations

import ctypes
import os
import sys

_RENAME_NOREPLACE = 1
_RENAME_EXCL_DARWIN = 4

# renameat2 syscall numbers for common Linux architectures.
_RENAMEAT2_NR = {
    "x86_64": 316,
    "aarch64": 276,
    "riscv64": 276,
    "i386": 353,
    "i686": 353,
    "armv6l": 382,
    "armv7l": 382,
    "s390x": 347,
}

_libc = ctypes.CDLL(None, use_errno=True)


def rename_noreplace(old: str, old_dirfd: int, new: str, new_dirfd: int) -> None:
    """Rename ``old`` to ``new`` atomically; raise FileExistsError if ``new``
    exists (even if it appeared after any userspace check)."""
    if sys.platform.startswith("linux"):
        machine = os.uname().machine
        nr = _RENAMEAT2_NR.get(machine)
        if nr is None:
            raise NotImplementedError(
                f"renameat2 syscall number unknown for architecture {machine!r}"
            )
        rc = _libc.syscall(
            ctypes.c_long(nr),
            ctypes.c_int(old_dirfd),
            ctypes.c_char_p(os.fsencode(old)),
            ctypes.c_int(new_dirfd),
            ctypes.c_char_p(os.fsencode(new)),
            ctypes.c_uint(_RENAME_NOREPLACE),
        )
        if rc != 0:
            err = ctypes.get_errno()
            raise OSError(err, os.strerror(err), new)
    elif sys.platform == "darwin":  # development convenience only
        rc = _libc.renameatx_np(
            ctypes.c_int(old_dirfd),
            ctypes.c_char_p(os.fsencode(old)),
            ctypes.c_int(new_dirfd),
            ctypes.c_char_p(os.fsencode(new)),
            ctypes.c_uint(_RENAME_EXCL_DARWIN),
        )
        if rc != 0:
            err = ctypes.get_errno()
            raise OSError(err, os.strerror(err), new)
    else:
        raise NotImplementedError(
            "atomic no-clobber rename requires Linux (renameat2)"
        )
