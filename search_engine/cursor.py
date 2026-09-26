"""游标编解码：不透明、防篡改、绑定查询摘要与索引版本。

游标格式为 ``<base64url(JSON)>.<HMAC-SHA256 十六进制签名>``，
JSON 负载字段：

- ``v``             游标结构版本号，当前恒为 1。
- ``index_version`` 游标所属的固定索引版本。
- ``query_digest``  查询摘要（规范化词项列表 + 排序规格的 SHA-256）。
- ``last_key``      键集分页位置 ``[score, doc_id]``，即上一页最后一条
                    的排序键；排序为 ``(-score, doc_id)`` 的全序。

页大小不写入游标：翻页位置完全由 ``last_key`` 决定，因此调用方中途
改变页大小也不会重复已读文档。
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json

CURSOR_STRUCTURE_VERSION = 1
SORT_SPEC = "score_desc,id_asc"


class CursorError(ValueError):
    """游标相关错误的基类。"""


class CursorTamperedError(CursorError):
    """游标无法解码或签名校验失败。"""


class CursorQueryMismatchError(CursorError):
    """游标被复用于另一个查询（查询摘要不一致），拒绝服务。"""


class CursorIndexExpiredError(CursorError):
    """游标绑定的索引版本已被明确失效，无法继续翻页。"""


def query_digest(terms: list[str]) -> str:
    """对规范化后的查询（词项列表 + 排序规格）计算摘要。"""
    canonical = json.dumps(
        {"terms": terms, "sort": SORT_SPEC},
        ensure_ascii=False,
        separators=(",", ":"),
    )
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def encode_cursor(payload: dict, secret: bytes) -> str:
    body = base64.urlsafe_b64encode(
        json.dumps(payload, ensure_ascii=False, sort_keys=True,
                   separators=(",", ":")).encode("utf-8")
    ).decode("ascii")
    signature = hmac.new(secret, body.encode("ascii"), hashlib.sha256).hexdigest()
    return f"{body}.{signature}"


def decode_cursor(cursor: str, secret: bytes) -> dict:
    try:
        body, signature = cursor.rsplit(".", 1)
    except ValueError:
        raise CursorTamperedError("游标格式非法") from None
    expected = hmac.new(secret, body.encode("ascii"), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(signature, expected):
        raise CursorTamperedError("游标签名校验失败")
    try:
        payload = json.loads(base64.urlsafe_b64decode(body.encode("ascii")))
    except Exception:
        raise CursorTamperedError("游标负载无法解码") from None
    if not isinstance(payload, dict) or payload.get("v") != CURSOR_STRUCTURE_VERSION:
        raise CursorTamperedError("游标结构版本不受支持")
    last_key = payload.get("last_key")
    if (
        not isinstance(payload.get("index_version"), int)
        or not isinstance(payload.get("query_digest"), str)
        or not (isinstance(last_key, list) and len(last_key) == 2)
    ):
        raise CursorTamperedError("游标负载字段缺失或类型错误")
    return payload
