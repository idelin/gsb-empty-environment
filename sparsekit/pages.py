"""Paged copy-on-write buffers and shared-storage statistics."""
from __future__ import annotations

from dataclasses import dataclass

DEFAULT_PAGE_SIZE = 64


@dataclass
class StorageStats:
    pages_created: int = 0
    pages_copied: int = 0
    pages_reused: int = 0
    views_created: int = 0
    elements_touched: int = 0

    def snapshot(self) -> "StorageStats":
        return StorageStats(**vars(self))

    def delta(self, earlier: "StorageStats") -> "StorageStats":
        return StorageStats(
            **{key: getattr(self, key) - getattr(earlier, key) for key in vars(self)}
        )


STATS = StorageStats()


def reset_stats() -> None:
    for key in vars(STATS):
        setattr(STATS, key, 0)


class Page:
    __slots__ = ("cells", "frozen")

    def __init__(self, cells, frozen: bool = True):
        self.cells = cells
        self.frozen = frozen


class PagedBuffer:
    """Immutable logical array backed by fixed-size frozen pages."""

    __slots__ = ("_pages", "_offset", "_len", "page_size")

    def __init__(self, pages, offset: int, length: int, page_size: int):
        self._pages = pages
        self._offset = offset
        self._len = length
        self.page_size = page_size

    @classmethod
    def empty(cls, page_size: int = DEFAULT_PAGE_SIZE) -> "PagedBuffer":
        return cls([], 0, 0, page_size)

    @classmethod
    def from_values(cls, values, page_size: int = DEFAULT_PAGE_SIZE) -> "PagedBuffer":
        values = list(values)
        if not values:
            return cls.empty(page_size)
        pages = []
        for start in range(0, len(values), page_size):
            chunk = list(values[start:start + page_size])
            if len(chunk) < page_size:
                chunk.extend([0] * (page_size - len(chunk)))
            pages.append(Page(chunk))
            STATS.pages_created += 1
        return cls(pages, 0, len(values), page_size)

    def __len__(self) -> int:
        return self._len

    def __getitem__(self, index: int):
        if not 0 <= index < self._len:
            raise IndexError(f"index {index} out of range {self._len}")
        pos = self._offset + index
        page_i, cell_i = divmod(pos, self.page_size)
        return self._pages[page_i].cells[cell_i]

    def to_list(self):
        return [self[i] for i in range(self._len)]

    @property
    def pages(self):
        return list(self._pages)

    def window(self, start: int, length: int) -> "PagedBuffer":
        if not 0 <= start <= self._len or not 0 <= length <= self._len - start:
            raise IndexError("window out of range")
        if length == 0:
            return PagedBuffer.empty(self.page_size)
        abs_start = self._offset + start
        first = abs_start // self.page_size
        last = (abs_start + length - 1) // self.page_size
        pages = self._pages[first:last + 1]
        STATS.pages_reused += len(pages)
        return PagedBuffer(
            pages, abs_start - first * self.page_size, length, self.page_size
        )

    def updated(self, index: int, value) -> "PagedBuffer":
        if not 0 <= index < self._len:
            raise IndexError(f"index {index} out of range {self._len}")
        pos = self._offset + index
        page_i, cell_i = divmod(pos, self.page_size)
        cells = list(self._pages[page_i].cells)
        cells[cell_i] = value
        pages = list(self._pages)
        pages[page_i] = Page(cells)
        STATS.pages_copied += 1
        STATS.elements_touched += self.page_size
        STATS.pages_reused += len(pages) - 1
        return PagedBuffer(pages, self._offset, self._len, self.page_size)

    def splice(self, start: int, delete: int, insert) -> "PagedBuffer":
        insert = list(insert)
        if not 0 <= start <= self._len or not 0 <= delete <= self._len - start:
            raise IndexError("splice out of range")
        ps = self.page_size
        new_len = self._len - delete + len(insert)
        if new_len == 0:
            return PagedBuffer.empty(ps)
        abs_start = self._offset + start
        src_abs = abs_start + delete
        dst_abs = abs_start + len(insert)
        shift = src_abs - dst_abs
        end_abs = self._offset + new_len
        head_pages = abs_start // ps
        tail_begin = -(-src_abs // ps)
        tail_dst_begin = tail_begin * ps - shift
        share_tail = (
            shift % ps == 0
            and tail_begin > head_pages
            and tail_dst_begin < end_abs
        )
        if not share_tail:
            tail_begin = len(self._pages)
            tail_dst_begin = end_abs
        shared_head = self._pages[:head_pages]
        shared_tail = self._pages[tail_begin:] if share_tail else []
        middle = []
        src_log = max(0, head_pages * ps - self._offset)
        while src_log < start:
            middle.append(self[src_log])
            src_log += 1
        middle.extend(insert)
        src_log = start + delete
        suffix_count = tail_dst_begin - dst_abs
        for _ in range(suffix_count):
            middle.append(self[src_log])
            src_log += 1
        while len(middle) % ps:
            middle.append(0)
        middle_pages = []
        for i in range(0, len(middle), ps):
            middle_pages.append(Page(middle[i:i + ps]))
            STATS.pages_created += 1
        STATS.pages_reused += len(shared_head) + len(shared_tail)
        return PagedBuffer(
            shared_head + middle_pages + shared_tail,
            self._offset,
            new_len,
            ps,
        )
