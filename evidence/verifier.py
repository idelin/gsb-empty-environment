"""Independent package verifier.

Reads an exported package with no knowledge of the receiver's database and
locates the cause of every failure: tampered content, reordered chunks or a
tampered manifest. Also replays the derivation log when present.
"""
from __future__ import annotations

import hashlib
import json
import os
from dataclasses import dataclass, field

from evidence.domain import decode_log, replay
from evidence.manifest import Manifest, iter_file_blocks


@dataclass
class Failure:
    code: str
    location: str
    detail: str


@dataclass
class VerificationReport:
    ok: bool
    failures: list[Failure] = field(default_factory=list)

    def codes(self) -> list[str]:
        return [item.code for item in self.failures]


DERIVATION_LOG_FILE = "derivation-log"


def verify_package(dest: str) -> VerificationReport:
    failures: list[Failure] = []
    manifest_path = os.path.join(dest, "manifest.json")
    signoff_path = os.path.join(dest, "signoff.json")

    try:
        with open(manifest_path, "rb") as fh:
            raw_manifest = fh.read()
        body = json.loads(raw_manifest.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        return VerificationReport(ok=False, failures=[Failure(
            code="MANIFEST_UNREADABLE", location="manifest.json",
            detail=str(exc))])

    recorded_digest = body.pop("digest", None)
    try:
        manifest = Manifest.from_dict(body)
    except (KeyError, ValueError, TypeError) as exc:
        return VerificationReport(ok=False, failures=[Failure(
            code="MANIFEST_MALFORMED", location="manifest.json",
            detail=str(exc))])

    if recorded_digest is None or recorded_digest != manifest.digest:
        failures.append(Failure(
            code="MANIFEST_TAMPERED", location="manifest.json",
            detail="overall digest of manifest body does not match"
                   f" (recorded={recorded_digest!r} actual={manifest.digest!r})"))

    try:
        with open(signoff_path, "r", encoding="utf-8") as fh:
            signoff = json.load(fh)
    except (OSError, json.JSONDecodeError):
        signoff = None
    if signoff is not None and signoff.get("manifest_digest") != manifest.digest:
        failures.append(Failure(
            code="SIGNOFF_MANIFEST_MISMATCH", location="signoff.json",
            detail="sign-off is bound to a different manifest digest"))

    verified_digests: dict[str, str] = {}
    for entry in manifest.files:
        location = f"files/{entry.file_id}"
        path = os.path.join(dest, "files", entry.file_id)
        if not os.path.isfile(path):
            failures.append(Failure(code="MISSING_FILE", location=location,
                                    detail="declared file absent from package"))
            continue
        size = os.path.getsize(path)
        if size != entry.size:
            failures.append(Failure(
                code="CONTENT_MISMATCH", location=location,
                detail=f"size declared={entry.size} actual={size}"))
            continue

        got_chunk_digests: list[str] = []
        running = hashlib.sha256()
        with open(path, "rb") as handle:
            for block in iter_file_blocks(handle, entry.chunk_size):
                got_chunk_digests.append(hashlib.sha256(block).hexdigest())
                running.update(block)
        whole = running.hexdigest()

        if whole == entry.file_digest:
            verified_digests[entry.file_id] = whole
            continue

        expected = list(entry.chunk_digests)
        if len(got_chunk_digests) != len(expected):
            failures.append(Failure(
                code="CONTENT_MISMATCH", location=location,
                detail=f"chunk count declared={len(expected)} "
                       f"actual={len(got_chunk_digests)}"))
            continue
        remaining = list(expected)
        for index, got in enumerate(got_chunk_digests):
            if got == expected[index]:
                continue
            if got in remaining and got != expected[index]:
                failures.append(Failure(
                    code="ORDER_MISMATCH", location=f"{location}#chunk{index}",
                    detail="chunk exists elsewhere in the sequence; chunks were"
                           f" reordered (got digest belonging to index "
                           f"{expected.index(got)})"))
            else:
                failures.append(Failure(
                    code="CONTENT_MISMATCH",
                    location=f"{location}#chunk{index}",
                    detail=f"chunk digest expected={expected[index]} got={got}"))
            break
        else:
            failures.append(Failure(
                code="CONTENT_MISMATCH", location=location,
                detail="file digest mismatch"))

    for ref in manifest.references:
        target = manifest.file_entry(ref.target_file_id)
        if target is None:
            failures.append(Failure(
                code="REFERENCE_TARGET_MISSING",
                location=f"files/{ref.source_file_id}",
                detail=f"reference target {ref.target_file_id} not in manifest"))
        elif ref.target_file_id not in verified_digests:
            failures.append(Failure(
                code="REFERENCE_TARGET_UNVERIFIED",
                location=f"files/{ref.source_file_id}",
                detail=f"target {ref.target_file_id} could not be verified"))

    log_path = os.path.join(dest, "files", DERIVATION_LOG_FILE)
    if os.path.isfile(log_path):
        location = f"files/{DERIVATION_LOG_FILE}"
        with open(log_path, "rb") as fh:
            raw_log = fh.read()
        try:
            steps = decode_log(raw_log)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            failures.append(Failure(
                code="DERIVATION_LOG_MALFORMED", location=location,
                detail=f"replay input unreadable: {exc}"))
        else:
            failures.extend(
                Failure(code=item["code"],
                        location=f"{location}#{item['step_id']}",
                        detail=item["detail"])
                for item in replay(steps))

    return VerificationReport(ok=not failures, failures=failures)
