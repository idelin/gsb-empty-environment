"""本地夹具加载：变更事件来自 JSON 文件，不接消息平台。"""
from __future__ import annotations

import json
from typing import List

from .events import ChangeEvent


def load_events(path: str) -> List[ChangeEvent]:
    with open(path, "r", encoding="utf-8") as fh:
        return [ChangeEvent.from_dict(d) for d in json.load(fh)]
