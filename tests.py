from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from dbk.graph import CycleDetected, NodeSpec
from dbk.kernel import BuildKernel, Dependency, RequestStatus, TaskContext
from dbk.store import SimulatedCrash
from dbk.reference import ReferenceExecutor
from dbk.tester import InterleaveTester
from dbk.vfs import VFS


def materialize(kernel: BuildKernel, request_id: str) -> None:
    kernel.run_until_idle()
    status = kernel.requests[request_id].status
    if status == RequestStatus.FAILED:
        raise kernel.requests[request_id].error
    if status != RequestStatus.COMPLETE:
        raise AssertionError(f"request ended {status}")


class DynamicBuildTests(unittest.TestCase):
    def make_dir(self) -> tempfile.TemporaryDirectory[str]:
        directory = tempfile.TemporaryDirectory()
        return directory

    def test_negative_dependency_and_directory_invalidation_scope(self) -> None:
        def negative(ctx: TaskContext) -> bytes:
            ctx.check_missing("inputs/missing.txt", checkpoint=False)
            return ctx.read_file("inputs/source.txt")

        def listing(ctx: TaskContext) -> str:
            entries = ctx.list_dir("dir")
            return ",".join(f"{entry.name}:{entry.type}" for entry in entries)

        with tempfile.TemporaryDirectory() as workdir:
            vfs = VFS(workdir)
            vfs.write("inputs/source.txt", "v1")
            vfs.write("dir/a.txt", "a")
            vfs.write("elsewhere.txt", "outside")
            specs = [
                NodeSpec("negative", negative, output="out/negative.bin"),
                NodeSpec("listing", listing, output="out/listing.txt", sort_rule="name"),
            ]
            kernel = BuildKernel(workdir, specs, env={})
            request = kernel.request_build(roots=["negative", "listing"])
            materialize(kernel, request)
            self.assertEqual(kernel.started_nodes, {"negative", "listing"})

            vfs.write("elsewhere.txt", "outside-change")
            second = kernel.request_build(roots=["negative", "listing"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[second].status, RequestStatus.COMPLETE)
            starts = [event["node"] for event in kernel.store.read_events() if event["event"] == "start"]
            self.assertEqual(sorted(starts), ["listing", "negative"])

            vfs.write("inputs/source.txt", "v2")
            third = kernel.request_build(roots=["negative"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[third].status, RequestStatus.COMPLETE)
            starts = [event["node"] for event in kernel.store.read_events() if event["event"] == "start"]
            self.assertEqual(starts[:2], ["listing", "negative"])
            self.assertEqual(starts[2:], ["negative"])
            self.assertEqual(vfs.read("out/negative.bin"), b"v2")

            vfs.write("inputs/missing.txt", "appeared")
            fourth = kernel.request_build(roots=["negative"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[fourth].status, RequestStatus.COMPLETE)
            starts = [event["node"] for event in kernel.store.read_events() if event["event"] == "start"]
            self.assertEqual(starts[-1], "negative")

            vfs.write("dir/b.txt", "new-entry")
            fifth = kernel.request_build(roots=["listing"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[fifth].status, RequestStatus.COMPLETE)
            self.assertEqual(vfs.read("out/listing.txt"), b"a.txt:file,b.txt:file")
            starts = [event["node"] for event in kernel.store.read_events() if event["event"] == "start"]
            self.assertEqual(starts.count("listing"), 2)

            vfs.write("dir/a.txt", "same-name-changed-content")
            sixth = kernel.request_build(roots=["listing"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[sixth].status, RequestStatus.COMPLETE)
            starts = [event["node"] for event in kernel.store.read_events() if event["event"] == "start"]
            self.assertEqual(starts.count("listing"), 2)
            kernel.close()

    def test_dynamic_cycle_returns_ordered_evidence(self) -> None:
        def a_task(ctx: TaskContext) -> bytes:
            ctx.read_file("switch.txt", checkpoint=False)
            ctx.checkpoint("after-read")
            ctx.node_dependencies.add("b")
            ctx.checkpoint("after-discover")
            return b"a"

        def b_task(ctx: TaskContext) -> bytes:
            ctx.read_file("switch.txt")
            ctx.node_dependencies.add("a")
            return b"b"

        with tempfile.TemporaryDirectory() as workdir:
            vfs = VFS(workdir)
            vfs.write("switch.txt", "on")
            specs = [
                NodeSpec("a", a_task, output="out/a"),
                NodeSpec("b", b_task, deps=("a",), output="out/b"),
            ]
            kernel = BuildKernel(workdir, specs, env={})
            request = kernel.request_build(roots=["a"])
            kernel.start_ready()
            kernel.wait_until_paused("a", "after-read")
            attempt = kernel.active_by_node["a"]
            attempt.pause.resume()
            kernel.wait_until_paused("a", "after-discover")
            attempt.pause.resume()
            attempt.thread.join(timeout=1)
            kernel._finalize_attempt(attempt)
            self.assertEqual(kernel.requests[request].status, RequestStatus.FAILED)
            error = kernel.requests[request].error
            self.assertIsInstance(error, CycleDetected)
            self.assertEqual(error.cycle[0], error.cycle[-1])
            self.assertEqual(set(error.cycle[:-1]), {"a", "b"})
            self.assertFalse(vfs.exists("out/a"))
            kernel.close()

    def test_shared_task_cancel_does_not_cancel_other_subscriber(self) -> None:
        def shared(ctx: TaskContext) -> bytes:
            data = ctx.read_file("shared-input.txt")
            ctx.checkpoint("after-read")
            return data + b"-out"

        with tempfile.TemporaryDirectory() as workdir:
            vfs = VFS(workdir)
            vfs.write("shared-input.txt", "payload")
            specs = [NodeSpec("shared", shared, output="out/shared")]
            kernel = BuildKernel(workdir, specs, env={})
            first = kernel.request_build("r1", ["shared"])
            second = kernel.request_build("r2", ["shared"])
            kernel.start_ready()
            kernel.wait_until_paused("shared", "after-read")
            self.assertEqual(kernel.active_by_node["shared"].request_ids, {"r1", "r2"})
            kernel.cancel_request(first)
            self.assertEqual(kernel.requests[first].status, RequestStatus.CANCELED)
            attempt = kernel.active_by_node["shared"]
            attempt.pause.resume()
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[second].status, RequestStatus.COMPLETE)
            self.assertEqual(vfs.read("out/shared"), b"payload-out")
            kernel.close()

    def test_commit_points_and_recovery(self) -> None:
        cases = [
            ("graph_commit_before", True),
            ("graph_commit_after", True),
            ("cache_commit_before", True),
            ("cache_commit_after", True),
            ("publish_commit_before", False),
            ("publish_commit_after", False),
        ]
        for crash_point, should_rerun in cases:
            with self.subTest(crash_point=crash_point):
                with tempfile.TemporaryDirectory() as workdir:
                    self._run_crash_case(workdir, crash_point, should_rerun)

    def _run_crash_case(self, workdir: str, crash_point: str, should_rerun: bool) -> None:
        dynamic_graph = crash_point.startswith("graph_")

        def discover(ctx: TaskContext) -> bytes:
            ctx.read_file("source.txt")
            ctx.node_dependencies.add("dep")
            return b"published"

        def simple(ctx: TaskContext) -> bytes:
            return ctx.read_file("source.txt")

        def dep(ctx: TaskContext) -> bytes:
            return ctx.read_file("dep-source.txt")

        vfs = VFS(workdir)
        vfs.write("source.txt", "source")
        vfs.write("dep-source.txt", "dep")
        a_task = discover if dynamic_graph else simple
        specs = [NodeSpec("a", a_task, output="out/a")]
        if dynamic_graph:
            specs.append(NodeSpec("dep", dep, output="out/dep"))
        kernel = BuildKernel(workdir, specs, env={}, crash_points=[crash_point])
        kernel.request_build(roots=["a"])
        try:
            kernel.run_until_idle(max_steps=100)
        except SimulatedCrash:
            pass
        started_before_crash = [
            event["node"]
            for event in kernel.store.read_events()
            if event["event"] == "start"
        ]
        self.assertEqual(started_before_crash, ["a"])
        kernel.close()
        temp_items = list((Path(workdir) / ".buildkernel" / "tmp").iterdir())
        self.assertEqual(temp_items, [])
        recovered = BuildKernel(workdir, specs, env={})
        recovered_request = recovered.request_build(roots=["a"])
        started_after_open = [
            event["node"]
            for event in recovered.store.read_events()
            if event["event"] == "start"
        ]
        self.assertEqual(started_after_open, ["a"])
        recovered.run_until_idle()
        self.assertEqual(recovered.requests[recovered_request].status, RequestStatus.COMPLETE)
        self.assertEqual(vfs.read("out/a"), b"published" if dynamic_graph else b"source")
        started_after_recovery = [
            event["node"]
            for event in recovered.store.read_events()
            if event["event"] == "start"
        ]
        recovered_starts = started_after_recovery[len(started_after_open):]
        if should_rerun:
            self.assertIn("a", recovered_starts)
        else:
            self.assertEqual(recovered_starts, [])
        if dynamic_graph:
            self.assertIn("dep", recovered_starts)
        recovered.close()

    def test_cache_identity_and_path_aliases(self) -> None:
        def task(ctx: TaskContext) -> bytes:
            argument = ctx.kernel.graph.nodes[ctx.node].args[0]
            return ctx.read_file("alias/input.txt") + argument.encode()

        with tempfile.TemporaryDirectory() as first_dir, tempfile.TemporaryDirectory() as second_dir:
            vfs = VFS(first_dir)
            vfs.write("alias/input.txt", "same")
            specs = [
                NodeSpec(
                    "node",
                    task,
                    output="out/node",
                    tool="tool-v1",
                    args=("x",),
                    env_whitelist=("DBK_TOOL_ENV",),
                )
            ]
            kernel = BuildKernel(
                first_dir,
                specs,
                env={"DBK_TOOL_ENV": "one"},
                aliases={"alias/input.txt": "logical-input"},
            )
            request = kernel.request_build(roots=["node"])
            materialize(kernel, request)
            first_key = kernel.caches["node"].cache_key
            vfs.write("elsewhere", "does-not-invalidate")
            second = kernel.request_build(roots=["node"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[second].status, RequestStatus.COMPLETE)
            self.assertEqual(kernel.caches["node"].cache_key, first_key)
            starts = [event["node"] for event in kernel.store.read_events() if event["event"] == "start"]
            self.assertEqual(starts, ["node"])

            vfs.copy_tree(second_dir)
            changed_specs = [
                NodeSpec(
                    "node",
                    task,
                    output="out/node",
                    tool="tool-v2",
                    args=("x",),
                    env_whitelist=("DBK_TOOL_ENV",),
                )
            ]
            changed_kernel = BuildKernel(
                second_dir,
                changed_specs,
                env={"DBK_TOOL_ENV": "one"},
                aliases={"alias/input.txt": "logical-input"},
            )
            changed_request = changed_kernel.request_build(roots=["node"])
            changed_kernel.run_until_idle()
            self.assertEqual(changed_kernel.requests[changed_request].status, RequestStatus.COMPLETE)
            self.assertEqual(
                [event["node"] for event in changed_kernel.store.read_events() if event["event"] == "start"],
                ["node"],
            )
            kernel.close()
            changed_kernel.close()

    def test_old_generation_cannot_commit_over_newer_attempt(self) -> None:
        def task(ctx: TaskContext) -> bytes:
            data = ctx.read_file("race.txt")
            ctx.checkpoint("after-read")
            return data + b":ok"

        with tempfile.TemporaryDirectory() as workdir:
            vfs = VFS(workdir)
            vfs.write("race.txt", "v1")
            specs = [NodeSpec("race", task, output="out/race")]
            kernel = BuildKernel(workdir, specs, env={})
            request = kernel.request_build(roots=["race"])
            kernel.start_ready()
            kernel.wait_until_paused("race", "after-read")
            old = kernel.active_by_node["race"]
            kernel.node_generation["race"] += 1
            old.pause.resume(stale=True)
            old.thread.join(timeout=1)
            kernel._finalize_attempt(old)
            self.assertNotIn("race", kernel.caches)
            vfs.write("race.txt", "v2")
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[request].status, RequestStatus.COMPLETE)
            self.assertEqual(vfs.read("out/race"), b"v2:ok")
            kernel.close()

    def test_unrelated_file_change_against_reference(self) -> None:
        def task(ctx: TaskContext) -> bytes:
            return ctx.read_file("only.txt") + b"-result"

        with tempfile.TemporaryDirectory() as initial_dir, tempfile.TemporaryDirectory() as reference_dir:
            vfs = VFS(initial_dir)
            vfs.write("only.txt", "one")
            specs = [NodeSpec("only-node", task, output="out/result")]
            kernel = BuildKernel(initial_dir, specs, env={})
            request = kernel.request_build(roots=["only-node"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[request].status, RequestStatus.COMPLETE)
            vfs.write("unrelated/new/file.txt", "does-not-matter")
            second = kernel.request_build(roots=["only-node"])
            kernel.run_until_idle()
            self.assertEqual(kernel.requests[second].status, RequestStatus.COMPLETE)
            starts = [event["node"] for event in kernel.store.read_events() if event["event"] == "start"]
            self.assertEqual(starts, ["only-node"])
            reference = ReferenceExecutor(vfs, specs).run(["only-node"], reference_dir)
            self.assertEqual(vfs.read("out/result"), reference.outputs["only-node"])
            kernel.close()

    def test_interleave_space_matches_reference_and_records_real_executions(self) -> None:
        tester = InterleaveTester()
        results = tester.run_all()
        self.assertEqual(len(results), len(tester.event_space()))
        for result in results:
            if not result.events or result.events[0] not in {"C@1", "C@3"}:
                self.assertEqual(result.request_status, "complete", result.events)
                self.assertEqual(result.output, result.reference_output, result.events)
            self.assertTrue(result.execution_sequence, result.events)


if __name__ == "__main__":
    unittest.main(verbosity=2)
