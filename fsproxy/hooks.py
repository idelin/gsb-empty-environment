"""Test/observability hooks.

The proxy calls these no-op methods at precisely defined points so tests can
use barriers to arrange deterministic interleavings (e.g. swapping a symlink
between the component check and the component open).  Production code should
simply use the default :class:`Hooks` instance.
"""


class Hooks:
    def after_component_check(self, *, name, parent_fd, stat_result, path_so_far):
        """Called after a path component was lstat()ed, before it is opened.

        This is the exact "check -> open" window a racer would attack.
        """

    def before_replace_commit(self, *, parent_fd, tmp_name, final_name):
        """Called after the temp file is fully written, before the atomic rename."""

    def before_rename_syscall(self, *, src_parent_fd, src_name, dst_parent_fd, dst_name):
        """Called after both rename paths are authorized, before rename(2)."""

    def on_write_chunk(self, *, fd, offset, chunk):
        """Called before each write() to a replace() temp file.

        Raise to inject a write failure; replace() must then clean up the
        partial temp file and leave the official output untouched.
        """
