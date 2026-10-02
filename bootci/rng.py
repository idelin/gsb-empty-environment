"""随机源：由调用方注入种子；按样本 ID 分流，保证并行/处理顺序无关。

派生使用 SHA-256（跨进程、跨平台确定），而非内置 hash()（受
PYTHONHASHSEED 影响）。每个 stream_id 得到独立可复现的随机流，
因此样本以任意顺序、任意并行度处理，结果完全一致。
"""
from __future__ import annotations

import hashlib
import random


def derive_seed(master_seed: int, stream_id: str) -> int:
    """由 (master_seed, stream_id) 确定性派生 128 位子种子。"""
    if isinstance(master_seed, bool) or not isinstance(master_seed, int):
        raise TypeError(f"master_seed 必须为 int，得到 {type(master_seed).__name__}")
    digest = hashlib.sha256(f"bootci|{master_seed}|{stream_id}".encode("utf-8")).digest()
    return int.from_bytes(digest[:16], "big")


class StreamFactory:
    """按 stream_id 派生独立随机流。流之间互不影响，与创建顺序无关。"""

    def __init__(self, master_seed: int):
        if isinstance(master_seed, bool) or not isinstance(master_seed, int):
            raise TypeError(f"master_seed 必须为 int，得到 {type(master_seed).__name__}")
        self.master_seed = master_seed

    def stream(self, stream_id: str) -> random.Random:
        return random.Random(derive_seed(self.master_seed, stream_id))
