"""增量集合差异对账协议。

两种核验模式
------------
probabilistic（概率模式 / 摘要剪枝）
  自根向下按层比较分层摘要，只下钻摘要不同的子树，最终只拉取分歧叶节点内
  的完整记录做逐条比较。碰撞概率由哈希函数决定；SHA-256 下可忽略，
  但恒定哈希（测试注入）下所有摘要恒等，会错误地“零差异剪枝”。
  协议对此显式给出碰撞预警，且永远不会仅凭摘要相等就宣称集合相同（精确结论
  必须由 exact 模式给出）。

exact（确定模式 / 显式精确核验）
  不依据摘要做任何剪枝：拉取对端全部记录，与本地逐条精确比较，
  给出确定性结论与完整可复核差异列表。恒定哈希下仍可发现全部差异。

方向
----
reconcile(local, remote) 以 local 为请求方，从 remote 单向拉取。
因同时交换“对端有而本地没有”的条目，单向拉取的差异列表已覆盖两侧；
Stats 描述 local <- remote 方向传输的消息与条目数。
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Literal, Optional

from .hasher import Hasher, Sha256Hasher
from .model import Record
from .store import VersionedStore
from .tree import DigestTree, looks_degenerate

Mode = Literal["probabilistic", "exact"]

DIFF_ONLY_REMOTE = "only_remote"
DIFF_ONLY_LOCAL = "only_local"
DIFF_VALUE = "value_mismatch"
DIFF_VERSION = "version_mismatch"
DIFF_TOMBSTONE_VERSION = "tombstone_version_mismatch"

DIFF_KINDS = (
    DIFF_ONLY_REMOTE,
    DIFF_ONLY_LOCAL,
    DIFF_VALUE,
    DIFF_VERSION,
    DIFF_TOMBSTONE_VERSION,
)


@dataclass(frozen=True)
class RecordView:
    """差异列表中的一侧记录视图（None 表示该侧不存在该键）。"""

    key: str
    version: Optional[int]
    deleted: bool
    value_b64: Optional[str]
    value: Optional[str] = None

    @staticmethod
    def of(record: Optional[Record]) -> Optional["RecordView"]:
        if record is None:
            return None
        value_b64 = None
        value_text = None
        if record.value is not None:
            import base64

            value_b64 = base64.b64encode(record.value).decode("ascii")
            try:
                value_text = record.value.decode("utf-8")
            except UnicodeDecodeError:
                value_text = None
        return RecordView(
            key=record.key,
            version=record.version,
            deleted=record.deleted,
            value_b64=value_b64,
            value=value_text,
        )


@dataclass(frozen=True)
class DiffEntry:
    """一条可复核差异：含差异类型与两侧完整记录视图。"""

    kind: str
    local: Optional[RecordView]
    remote: Optional[RecordView]
    leaf_prefix: str
    detail: str

    @property
    def key(self) -> str:
        view = self.remote if self.remote is not None else self.local
        assert view is not None
        return view.key

    def to_json(self) -> dict[str, Any]:
        return {
            "kind": self.kind,
            "key": self.key,
            "leaf_prefix": self.leaf_prefix,
            "detail": self.detail,
            "local": None if self.local is None else self.local.__dict__,
            "remote": None if self.remote is None else self.remote.__dict__,
        }


@dataclass
class Stats:
    """传输统计：记录本地 <- 对端方向实际请求/返回的消息与条目数。

    digest_requests : 摘要请求消息数（概率模式逐层批处理）
    records_requests: 记录请求消息数
    digest_nodes_sent: 对端回传的摘要节点数
    records_sent    : 对端回传的完整记录条目数（含墓碑）
    bytes_sent      : 各响应负载的规范化编码字节数之和（估算）
    """

    digest_requests: int = 0
    records_requests: int = 0
    digest_nodes_sent: int = 0
    records_sent: int = 0
    bytes_sent: int = 0
    mode: Mode = "probabilistic"

    def to_json(self) -> dict[str, Any]:
        return {
            "mode": self.mode,
            "digest_requests": self.digest_requests,
            "records_requests": self.records_requests,
            "digest_nodes_sent": self.digest_nodes_sent,
            "records_sent": self.records_sent,
            "bytes_sent": self.bytes_sent,
        }


@dataclass
class ReconciliationResult:
    """对账结果：确定性结论、可复核差异列表、统计、模式与预警。"""

    equal: bool
    mode: Mode
    hash_name: str
    depth: int
    local_root: str
    remote_root: str
    diffs: list[DiffEntry] = field(default_factory=list)
    stats: Stats = field(default_factory=Stats)
    warnings: list[str] = field(default_factory=list)

    @property
    def exact_verdict(self) -> bool:
        """该结果的“相等”结论是否确定成立（仅精确模式可作确定结论）。"""
        return self.mode == "exact" and self.equal

    def to_json(self) -> dict[str, Any]:
        return {
            "equal": self.equal,
            "exact_verdict": self.exact_verdict,
            "mode": self.mode,
            "hash": self.hash_name,
            "depth": self.depth,
            "root_local": self.local_root,
            "root_remote": self.remote_root,
            "diff_count": len(self.diffs),
            "diffs": [entry.to_json() for entry in self.diffs],
            "stats": self.stats.to_json(),
            "warnings": self.warnings,
        }

    def dump_json(self) -> str:
        return json.dumps(self.to_json(), ensure_ascii=False, indent=2, sort_keys=True)


class Peer:
    """对端：包装一份存储，模拟可远程访问的摘要/记录接口。

    每个被调用的接口方法对应一次网络消息，Stats 在调用点累计。
    """

    def __init__(
        self,
        store: VersionedStore,
        hasher: Optional[Hasher] = None,
        depth: int = 2,
        label: str = "peer",
    ) -> None:
        self.store = store
        self.hasher = hasher if hasher is not None else Sha256Hasher()
        self.depth = depth
        self.label = label
        self.tree = DigestTree(self.store.records(), self.hasher, depth)

    # --- 远端接口（每次调用 = 一条请求/响应消息） ---

    def serve_root(self) -> str:
        return self.tree.root_digest()

    def serve_digests(self, prefixes: list[str]) -> dict[str, str]:
        return {prefix: self.tree.node_digest(prefix) for prefix in prefixes}

    def serve_records(self, prefixes: Optional[list[str]]) -> list[Record]:
        """prefixes 为 None 表示请求全部记录（精确模式）。"""
        if prefixes is None:
            return self.store.records()
        records: list[Record] = []
        for prefix in prefixes:
            records.extend(self.tree.leaf_records(prefix))
        return records


def _payload_bytes(obj: Any) -> int:
    return len(json.dumps(obj, ensure_ascii=False, sort_keys=True).encode("utf-8"))


def _classify(
    key: str,
    local: Optional[Record],
    remote: Optional[Record],
    leaf_prefix: str,
) -> DiffEntry:
    if remote is None:
        return DiffEntry(
            kind=DIFF_ONLY_LOCAL,
            local=RecordView.of(local),
            remote=None,
            leaf_prefix=leaf_prefix,
            detail="键仅存在于本地（对端无此键，含墓碑意义上的不存在）",
        )
    if local is None:
        return DiffEntry(
            kind=DIFF_ONLY_REMOTE,
            local=None,
            remote=RecordView.of(remote),
            leaf_prefix=leaf_prefix,
            detail="键仅存在于对端",
        )
    if local.version != remote.version:
        if local.deleted != remote.deleted:
            return DiffEntry(
                kind=DIFF_TOMBSTONE_VERSION,
                local=RecordView.of(local),
                remote=RecordView.of(remote),
                leaf_prefix=leaf_prefix,
                detail="一侧为删除墓碑、另一侧为存活值，且版本不同；按 LWW 高版本胜出合并",
            )
        return DiffEntry(
            kind=DIFF_VERSION,
            local=RecordView.of(local),
            remote=RecordView.of(remote),
            leaf_prefix=leaf_prefix,
            detail="两侧均存活或均墓碑但版本不同；按 LWW 高版本胜出合并",
        )
    if local.value != remote.value:
        return DiffEntry(
            kind=DIFF_VALUE,
            local=RecordView.of(local),
            remote=RecordView.of(remote),
            leaf_prefix=leaf_prefix,
            detail="键与版本相同但值不同（并发写入）；需确定性平局裁决",
        )
    raise AssertionError("无差异不应产生 DiffEntry")


def _build_diffs(
    local_records: list[Record],
    remote_records: list[Record],
    local_tree: DigestTree,
) -> list[DiffEntry]:
    """在给定范围内逐条精确比较，产出可复核差异列表。

    local_records 为本次核验覆盖的本地记录（概率模式=分歧叶子内的记录，
    精确模式=全量）；remote_records 为对端实际回传的记录。
    """
    local_map = {record.key: record for record in local_records}
    remote_map = {record.key: record for record in remote_records}
    keys = set(local_map) | set(remote_map)
    diffs: list[DiffEntry] = []
    for key in sorted(keys):
        local_record = local_map.get(key)
        remote_record = remote_map.get(key)
        if local_record is None and remote_record is None:
            continue
        if (
            local_record is not None
            and remote_record is not None
            and local_record == remote_record
        ):
            continue
        prefix = local_tree.leaf_prefix(key)
        diffs.append(_classify(key, local_record, remote_record, prefix))
    return diffs


def _reconcile_probabilistic(
    local: VersionedStore,
    remote: Peer,
    local_tree: DigestTree,
) -> tuple[list[DiffEntry], Stats, list[str]]:
    stats = Stats(mode="probabilistic")
    warnings: list[str] = []
    if looks_degenerate(remote.hasher):
        warnings.append(
            "哈希器对不同输入给出相同摘要（退化/恒定哈希）：摘要剪枝不可信，"
            "本结果可能漏报差异；相等结论仅为概率性，且当前哈希下碰撞概率为 1。"
            "请改用 mode=exact 获得确定性核验。"
        )

    depth = local_tree.depth
    mismatch_leaves: list[str] = []

    frontier = [""]
    for level in range(depth):
        if not frontier:
            break
        stats.digest_requests += 1
        remote_digests = remote.serve_digests(frontier)
        stats.digest_nodes_sent += len(remote_digests)
        stats.bytes_sent += _payload_bytes(remote_digests)
        next_frontier: list[str] = []
        for prefix in frontier:
            if local_tree.node_digest(prefix) != remote_digests[prefix]:
                next_frontier.extend(DigestTree.child_prefixes(prefix))
        frontier = next_frontier
    mismatch_leaves = sorted(frontier)

    remote_records: list[Record] = []
    if mismatch_leaves:
        stats.records_requests += 1
        remote_records = remote.serve_records(mismatch_leaves)
        stats.records_sent += len(remote_records)
        stats.bytes_sent += _payload_bytes([r.to_json() for r in remote_records])

    local_records: list[Record] = []
    for prefix in mismatch_leaves:
        local_records.extend(local_tree.leaf_records(prefix))
    diffs = _build_diffs(local_records, remote_records, local_tree)
    return diffs, stats, warnings


def _reconcile_exact(
    local: VersionedStore,
    remote: Peer,
    local_tree: DigestTree,
) -> tuple[list[DiffEntry], Stats, list[str]]:
    stats = Stats(mode="exact")
    warnings: list[str] = []
    if looks_degenerate(remote.hasher):
        warnings.append(
            "对端使用退化/恒定哈希；精确模式不依赖摘要剪枝，仍逐条核验全部记录，"
            "结论确定有效。"
        )
    stats.records_requests += 1
    remote_records = remote.serve_records(None)
    stats.records_sent += len(remote_records)
    stats.bytes_sent += _payload_bytes([r.to_json() for r in remote_records])
    diffs = _build_diffs(local.records(), remote_records, local_tree)
    return diffs, stats, warnings


def reconcile(
    local: VersionedStore,
    remote: Peer,
    mode: Mode = "probabilistic",
    hasher: Optional[Hasher] = None,
    depth: Optional[int] = None,
) -> ReconciliationResult:
    """执行一次本地 <- 对端的增量差异对账。

    hasher/depth 用于本地摘要树；对端的哈希器与深度由 remote 自身决定，
    两端应配置一致的哈希器与深度（精确模式不依赖它们）。
    """
    if mode not in ("probabilistic", "exact"):
        raise ValueError("mode 必须为 'probabilistic' 或 'exact'")
    effective_hasher = hasher if hasher is not None else remote.hasher
    effective_depth = depth if depth is not None else remote.depth
    local_tree = DigestTree(local.records(), effective_hasher, effective_depth)

    stats = Stats(mode=mode)
    # 根摘要始终先交换一次（即使退化哈希也不省略，便于人工复核与预警）
    remote_root = remote.serve_root()
    stats.bytes_sent += _payload_bytes({"root": remote_root})
    local_root = local_tree.root_digest()

    if mode == "probabilistic":
        diffs, sub_stats, warnings = _reconcile_probabilistic(local, remote, local_tree)
    else:
        diffs, sub_stats, warnings = _reconcile_exact(local, remote, local_tree)
    stats.digest_requests += sub_stats.digest_requests
    stats.records_requests += sub_stats.records_requests
    stats.digest_nodes_sent += sub_stats.digest_nodes_sent
    stats.records_sent += sub_stats.records_sent
    stats.bytes_sent += sub_stats.bytes_sent

    return ReconciliationResult(
        equal=not diffs,
        mode=mode,
        hash_name=effective_hasher.name,
        depth=effective_depth,
        local_root=local_root,
        remote_root=remote_root,
        diffs=diffs,
        stats=stats,
        warnings=warnings,
    )
