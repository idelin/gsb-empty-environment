#!/usr/bin/env python3.11
"""Offline runnable demo of the multi-output build cache.

Run with::

    python3.11 examples/run_demo.py
"""

from __future__ import annotations

import concurrent.futures
import pathlib
import sys
import tempfile
import threading

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from buildcache import (  # noqa: E402
    DigestMismatchError,
    MissingOutputError,
    NonDeterministicConflictError,
    SimulatorBuilder,
    Version,
    load_manifest,
)
from buildcache.engine import ActionCache, BuildNode  # noqa: E402
from buildcache.manifest import BuildOutput  # noqa: E402
from buildcache.store import ContentAddressableStore, sha256_hex  # noqa: E402

MANIFEST_PATH = pathlib.Path(__file__).with_name("manifest.json")


def heading(text: str) -> None:
    print(f"\n=== {text} ===")


def show_result(label: str, result) -> None:
    print(f"  [{label}] status={result.status} attempt={result.attempt_id[:8]}")
    for path, digest in result.outputs.items():
        print(f"      {path:<22} sha256={digest[:16]}...")


def main() -> None:
    heading("0. 版本号 major.minor.patch 与预发布（本地解析，不联网）")
    v1 = Version.parse("2.4.1-rc.1")
    v2 = Version.parse("2.4.1")
    v3 = Version.parse("0.3.0-beta.2")
    print(f"  {v1} 预发布={v1.is_prerelease}  < {v2}: {v1 < v2}")
    print(f"  排序: {sorted([v2, v3, v1])}")

    manifest = load_manifest(MANIFEST_PATH)
    heading("1. 本地 JSON 中的包与多输出构建节点")
    for pkg in manifest.packages.values():
        tag = " (预发布)" if pkg.is_prerelease else ""
        print(f"  package {pkg.name:<10} version={pkg.version}{tag}")
    for node in manifest.nodes.values():
        outputs = ", ".join(o.path for o in node.outputs)
        print(f"  node {node.key:<26} behavior={node.behavior:<15} outputs=[{outputs}]")

    with tempfile.TemporaryDirectory(prefix="buildcache-demo-") as tmp:
        store = ContentAddressableStore(tmp)
        builder = SimulatorBuilder()
        cache = ActionCache(store)

        heading("2. 首次构建 //core:release（多输出全部校验后一次性发布）")
        core = manifest.nodes["//core:release"]
        r1 = cache.request_build(core, manifest, builder)
        show_result("first", r1)

        heading("3. 再次构建 //core:release（同键同内容 -> 去重/命中）")
        r2 = cache.request_build(core, manifest, builder)
        show_result("again", r2)
        print(f"  同一 attempt_id => {r1.attempt_id == r2.attempt_id}（没有重复执行）")

        heading("4. 并发 5 个相同构建（同键同内容 -> 单飞去重，只执行一次）")
        barrier = threading.Barrier(5)

        def concurrent_same() -> str:
            barrier.wait()
            return cache.request_build(core, manifest, builder).attempt_id

        with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
            ids = list(pool.map(lambda _: concurrent_same(), range(5)))
        print(f"  5 个并发请求的 attempt_id 唯一数 = {len(set(ids))}（应为 1）")

        heading("5. 缺失一个输出：//tools:missing-output")
        missing = manifest.nodes["//tools:missing-output"]
        try:
            cache.request_build(missing, manifest, builder)
        except MissingOutputError as exc:
            print(f"  捕获 {type(exc).__name__}: {exc}")
        print(f"  条目是否发布 = {cache.read(missing.key) is not None}（一个输出缺失，其余也不发布）")

        heading("6. 摘要校验失败：//tools:bad-digest")
        bad = manifest.nodes["//tools:bad-digest"]
        try:
            cache.request_build(bad, manifest, builder)
        except DigestMismatchError as exc:
            print(f"  捕获 {type(exc).__name__}: {exc}")
        print(f"  条目是否发布 = {cache.read(bad.key) is not None}（校验失败即放弃并清理）")

        heading("7. 非确定性：同键不同内容不能后写覆盖前写")
        flaky = manifest.nodes["//flaky:image"]
        f1 = cache.request_build(flaky, manifest, builder)
        show_result("build #1", f1)
        try:
            cache.request_build(flaky, manifest, builder)
        except NonDeterministicConflictError as exc:
            print(f"  捕获 {type(exc).__name__}")
            print(f"    已发布摘要 = {exc.existing_digest[:24]}...")
            print(f"    后构建摘要 = {exc.produced_digest[:24]}...")
        kept = cache.read(flaky.key)
        print(f"  保留的是首次结果 => {kept['attempt_id'] == f1.attempt_id}（先写者胜出）")

        heading("8. 并发不同键互不阻塞，各自独立发布")

        def build_other(key: str):
            node = manifest.nodes[key]
            return key, cache.request_build(node, manifest, builder).status

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            for key, status in pool.map(
                build_other, ["//core:release", "//tools:digests-pinned"]
            ):
                print(f"  {key:<26} -> {status}")

        heading("9. 声明摘要的节点走快速命中路径")
        pinned_key = "//tools:digests-pinned"
        pinned = manifest.nodes[pinned_key]
        expected = {
            o.path: sha256_hex(o.render({"NAME": "data-tool", "VERSION": "1.0.0", "BUILD_PROFILE": "release"}))
            for o in pinned.outputs
        }
        pinned_checked = BuildNode(
            key=pinned.key,
            package=pinned.package,
            inputs=pinned.inputs,
            outputs=tuple(
                BuildOutput(path=o.path, template=o.template, encoding=o.encoding, sha256=expected[o.path])
                for o in pinned.outputs
            ),
            behavior=pinned.behavior,
        )
        pinned_result = cache.request_build(pinned_checked, manifest, builder)
        show_result("declared-sha256", pinned_result)
        print(f"  status=cached => {pinned_result.status == 'cached'}（摘要匹配，直接命中）")

        heading("10. 最终缓存状态")
        for key, entry in ((k, cache.read(k)) for k in manifest.nodes):
            if entry is None:
                print(f"  {key:<26} -> 未发布")
            else:
                print(
                    f"  {key:<26} -> {len(entry['outputs'])} 个输出 "
                    f"attempt={entry['attempt_id'][:8]}"
                )


if __name__ == "__main__":
    main()
