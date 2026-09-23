"""物化结果的存储：内存缓冲或溢写临时文件。

原子性保证：``put`` 先把结果完整写入私有缓冲（内存 list 或临时文件），
全部成功后才登记到 ``_entries``；中途失败会清理临时文件且不留下任何
可见条目——读者永远看不到"半张表"。
"""

import os
import pickle
import tempfile


class MaterializedStore:
    def __init__(self, spill_dir=None):
        self._entries = {}
        self._own_dir = spill_dir is None
        self._spill_dir = spill_dir or tempfile.mkdtemp(
            prefix='subplan_spill_')

    @property
    def spill_dir(self):
        return self._spill_dir

    def put(self, key, rows, spill):
        """物化 ``rows``（迭代器）。异常时不产生任何可见状态。"""
        if spill:
            fd, path = tempfile.mkstemp(dir=self._spill_dir,
                                        prefix='mat_', suffix='.bin')
            try:
                with os.fdopen(fd, 'wb') as handle:
                    for row in rows:
                        pickle.dump(tuple(row), handle)
            except BaseException:
                if os.path.exists(path):
                    os.unlink(path)
                raise
            self._entries[key] = ('spill', path)
        else:
            buffer = [tuple(row) for row in rows]  # 中途异常则不会登记
            self._entries[key] = ('mem', buffer)

    def get(self, key):
        """读取已物化的结果；不存在时抛 KeyError（绝不返回部分数据）。"""
        kind, payload = self._entries[key]
        if kind == 'mem':
            return iter(list(payload))
        return self._iter_spill(payload)

    @staticmethod
    def _iter_spill(path):
        with open(path, 'rb') as handle:
            while True:
                try:
                    yield pickle.load(handle)
                except EOFError:
                    return

    def discard(self, key):
        kind, payload = self._entries.pop(key)
        if kind == 'spill' and os.path.exists(payload):
            os.unlink(payload)

    def __contains__(self, key):
        return key in self._entries

    def close(self):
        for key in list(self._entries):
            self.discard(key)
        if self._own_dir and os.path.isdir(self._spill_dir):
            os.rmdir(self._spill_dir)

    def __enter__(self):
        return self

    def __exit__(self, *exc_info):
        self.close()
        return False
