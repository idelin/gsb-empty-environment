"""Synthetic, deterministic sample evidence and test helpers."""
from __future__ import annotations

import hashlib
import random

from evidence.domain import Fact, Step, encode_log
from evidence.manifest import build_manifest, iter_blocks
from evidence.receiver import Chunk


def synth_bytes(size: int, seed: bytes = b"evidence") -> bytes:
    """Deterministic pseudo-random bytes (no random module state needed)."""
    out = bytearray()
    counter = 0
    while len(out) < size:
        out += hashlib.sha256(seed + counter.to_bytes(8, "big")).digest()
        counter += 1
    return bytes(out[:size])


def _steps_for(facts: list[Fact]) -> list[Step]:
    values = facts
    mean = Step(
        step_id="step-mean", version=1, rule_id="mean", rule_version=1,
        inputs=tuple(values),
        output=Fact("fact-mean", 1,
                    round(sum(f.value for f in values) / len(values), 6)),
    )
    maximum = Step(
        step_id="step-max", version=1, rule_id="max", rule_version=1,
        inputs=tuple(values),
        output=Fact("fact-max", 1, max(f.value for f in values)),
    )
    minimum = Step(
        step_id="step-min", version=1, rule_id="min", rule_version=1,
        inputs=tuple(values),
        output=Fact("fact-min", 1, min(f.value for f in values)),
    )
    return [mean, maximum, minimum]


def readings_facts(tag: str = "v1") -> list[Fact]:
    # Synthetic sensor readings derived from a fixed seed.
    digest = hashlib.sha256(tag.encode()).digest()
    return [
        Fact(f"reading-{i}", 1,
             round(20.0 + (digest[i % len(digest)] + i * 7) % 150 / 10.0, 2))
        for i in range(8)
    ]


def build_files(tag: str, include_ghost: bool = False) -> dict[str, bytes]:
    facts = readings_facts(tag)
    steps = _steps_for(facts)
    log = encode_log(steps)
    csv_lines = ["fact_id,version,value"]
    csv_lines += [f"{f.fact_id},{f.version},{f.value}" for f in facts]
    readings = ("\n".join(csv_lines) + "\n").encode("utf-8")
    mean_out = steps[0].output
    report = (
        f"# Synthetic evidence report ({tag})\n\n"
        f"mean of {len(facts)} synthetic readings = {mean_out.value}\n"
        f"see readings.csv and derivation-log for replayable derivation.\n"
    ).encode("utf-8")
    files = {
        "readings.csv": readings,
        "derivation-log": log,
        "report.md": report,
    }
    if include_ghost:
        pass  # ghost reference is injected at manifest level, not as a file
    return files


def sample_references() -> list[tuple[str, str]]:
    return [("report.md", "readings.csv"), ("report.md", "derivation-log")]


def build_sample_manifest(manifest_id: str = "case-001", version: int = 1,
                          chunk_size: int = 64 * 1024,
                          ghost_reference: bool = False):
    tag = f"{manifest_id}:v{version}"
    files = build_files(tag)
    refs = sample_references()
    manifest = build_manifest(manifest_id, version, files, refs, chunk_size)
    if ghost_reference:
        from dataclasses import replace
        from evidence.manifest import Reference, sha256_hex
        ghost = Reference(source_file_id="report.md",
                          target_file_id="missing-evidence.csv",
                          target_digest=sha256_hex(b"ghost"))
        manifest = replace(manifest, references=manifest.references + (ghost,))
    return manifest, files


def chunks_for(manifest, files: dict[str, bytes]) -> list[Chunk]:
    result = []
    for entry in manifest.files:
        for index, block in enumerate(
                iter_blocks(files[entry.file_id], entry.chunk_size)):
            result.append(Chunk(manifest.manifest_id, manifest.version,
                                entry.file_id, index, block))
    return result


def feed_out_of_order(receiver, manifest, files: dict[str, bytes],
                      seed: int = 1234) -> list[str]:
    chunks = chunks_for(manifest, files)
    random.Random(seed).shuffle(chunks)
    return [receiver.receive_chunk(chunk) for chunk in chunks]
