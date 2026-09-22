from __future__ import annotations

import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

from .graph import NodeSpec
from .kernel import BuildKernel, Dependency, RequestStatus, TaskContext
from .reference import ReferenceExecutor
from .vfs import VFS


class TesterError(AssertionError):
    pass


def dynamic_discovering_task(ctx: TaskContext) -> str:
    ctx.read_files["in.txt"] = Dependency(
        "file", ctx.logical_id("in.txt"), ctx.kernel.file_version("in.txt")
    )
    ctx.checkpoint("after-read")
    if ctx.vfs.exists("trigger/newdep"):
        ctx.read_files["dep.txt"] = Dependency(
            "file", ctx.logical_id("dep.txt"), ctx.kernel.file_version("dep.txt")
        )
        ctx.checkpoint("after-discover")
        ctx.checkpoint("before-commit")
        return ctx.vfs.read("in.txt").decode() + "+" + ctx.vfs.read("dep.txt").decode()
    ctx.missing_paths["trigger/newdep"] = Dependency(
        "missing", ctx.logical_id("trigger/newdep"), "missing:0"
    )
    ctx.checkpoint("before-commit")
    return ctx.vfs.read("in.txt").decode() + "-none"


def interleave_specs():
    return {"a": NodeSpec("a", dynamic_discovering_task, output="out/a.txt")}


@dataclass(frozen=True)
class InterleaveResult:
    events: tuple[str, ...]
    request_status: str
    output: bytes | None
    execution_sequence: tuple[str, ...]
    distinct_executed: set[str]
    reference_output: bytes | None


class InterleaveTester:
    def event_space(self) -> list[tuple[str, ...]]:
        return [
            (),
            ("M@1",),
            ("M@2",),
            ("M@3",),
            ("X@1",),
            ("M@4",),
            ("X@4",),
            ("M@4", "M@5"),
            ("M@4", "M@6"),
            ("C@1",),
            ("C@3",),
        ]

    def run_case(self, events: Iterable[str]) -> tuple[InterleaveResult, BuildKernel]:
        event_tuple = tuple(events)
        tempdir = tempfile.TemporaryDirectory()
        root = Path(tempdir.name)
        vfs = VFS(root)
        vfs.write("in.txt", "old")
        specs = interleave_specs()
        kernel = BuildKernel(root, list(specs.values()), env={})
        request = kernel.request_build("req-a", ["a"])
        try:
            self._start_wait(kernel, "after-read")
            self._apply(event_tuple, 1, kernel)
            if kernel.requests[request].status == RequestStatus.WAITING:
                self._advance_to(kernel, "before-commit", ("after-discover",))
                self._apply(event_tuple, 2, kernel)
                self._finish(kernel, event_tuple, 3)
            self._drain_restart(kernel)
            canceled = any(item.startswith("C@") for item in event_tuple)
            if not canceled:
                self._second_phase(kernel, vfs, event_tuple)
            output = vfs.read("out/a.txt") if vfs.exists("out/a.txt") else None
            status = kernel.requests[request].status
            reference = ReferenceExecutor(vfs, list(specs.values())).run(
                ["a"], Path(tempfile.mkdtemp())
            )
            if not canceled and status != RequestStatus.COMPLETE:
                raise TesterError(f"{event_tuple}: expected complete, got {status}")
            if not canceled and output != reference.outputs.get("a"):
                raise TesterError(
                    f"{event_tuple}: {output!r} differs from reference {reference.outputs.get('a')!r}"
                )
            sequence = tuple(event["node"] for event in kernel.store.read_events() if event["event"] == "start")
            result = InterleaveResult(
                event_tuple,
                status.value,
                output,
                sequence,
                set(sequence),
                reference.outputs.get("a"),
            )
            return result, kernel
        finally:
            kernel.close()
            tempdir.cleanup()

    def run_all(self) -> list[InterleaveResult]:
        return [self.run_case(events)[0] for events in self.event_space()]

    def _start_wait(self, kernel: BuildKernel, target: str) -> None:
        kernel._start_ready_nodes()
        attempt = kernel.active_by_node["a"]
        attempt.pause.wait_until_paused()
        kernel.wait_until_paused("a", target)

    def _advance_to(
        self,
        kernel: BuildKernel,
        target: str,
        alternative: tuple[str, ...] = (),
    ) -> None:
        attempt = kernel.active_by_node["a"]
        expected = {target, *alternative}
        for _ in range(8):
            attempt.pause.wait_until_paused()
            if attempt.pause.name in expected:
                return
            attempt.pause.resume(stale=kernel._attempt_is_stale(attempt))
        raise AssertionError(f"did not reach {target}")

    def _finish(self, kernel: BuildKernel, events: tuple[str, ...], slot: int) -> None:
        request = kernel.requests["req-a"]
        if request.status != RequestStatus.WAITING or "a" not in kernel.active_by_node:
            return
        attempt = kernel.active_by_node["a"]
        attempt.pause.wait_until_paused()
        if attempt.pause.name != "before-commit":
            self._advance_to(kernel, "before-commit", ("after-discover",))
        self._apply(events, slot, kernel)
        if attempt.pause.waiting:
            attempt.pause.resume(stale=kernel._attempt_is_stale(attempt))
        attempt.thread.join(timeout=1)
        kernel._finalize_attempt(attempt)
        kernel._complete_cached_requests()

    def _drain_restart(self, kernel: BuildKernel) -> None:
        while kernel.requests["req-a"].status == RequestStatus.WAITING:
            before = len(kernel.active_by_node)
            kernel._start_ready_nodes()
            if not kernel.active_by_node:
                kernel._complete_cached_requests()
                return
            attempt = kernel.active_by_node["a"]
            attempt.pause.wait_until_paused()
            self._advance_to(kernel, "before-commit", ("after-discover",))
            attempt.pause.resume(stale=kernel._attempt_is_stale(attempt))
            attempt.thread.join(timeout=1)
            kernel._finalize_attempt(attempt)
            kernel._complete_cached_requests()
            if len(kernel.active_by_node) == before and kernel.requests["req-a"].status == RequestStatus.WAITING:
                raise RuntimeError("restart did not progress")

    def _second_phase(self, kernel: BuildKernel, vfs: VFS, events: tuple[str, ...]) -> None:
        if any(event in {("M@4",), ("X@4",), ("M@4", "M@5"), ("M@4", "M@6")} for event in [events]):
            self._apply(events, 4, kernel)
        second = kernel.request_build("req-b", ["a"])
        if ("M@4", "M@5") == events:
            self._start_wait(kernel, "after-read")
            self._apply(events, 5, kernel)
            self._finish_until_settled(kernel, "req-b")
        elif ("M@4", "M@6") == events:
            self._start_wait(kernel, "after-read")
            self._advance_to(kernel, "before-commit")
            self._apply(events, 6, kernel)
            self._finish_until_settled(kernel, "req-b")
        else:
            kernel.run_until_idle()
        if kernel.requests[second].status != RequestStatus.COMPLETE:
            raise TesterError(f"{events}: second request {kernel.requests[second].status}")

    def _finish_until_settled(self, kernel: BuildKernel, request_id: str) -> None:
        guard = 0
        while kernel.requests[request_id].status == RequestStatus.WAITING:
            guard += 1
            if guard > 8:
                raise RuntimeError("controlled second phase did not settle")
            if "a" in kernel.active_by_node:
                attempt = kernel.active_by_node["a"]
                attempt.pause.wait_until_paused()
                if attempt.pause.name != "before-commit":
                    self._advance_to(kernel, "before-commit", ("after-discover",))
                attempt.pause.resume(stale=kernel._attempt_is_stale(attempt))
                attempt.thread.join(timeout=1)
                kernel._finalize_attempt(attempt)
            kernel._complete_cached_requests()
            kernel._start_ready_nodes()

    def _apply(self, events: tuple[str, ...], slot: int, kernel: BuildKernel) -> None:
        for event in events:
            kind, text = event.split("@")
            if int(text) != slot:
                continue
            if kind == "M":
                kernel.vfs.write("in.txt", f"new-{slot}")
            elif kind == "X":
                kernel.vfs.mkdir("trigger")
                kernel.vfs.write("trigger/newdep", "on")
                kernel.vfs.write("dep.txt", "dep")
            elif kind == "C":
                kernel.cancel_request("req-a")
