"""Chunk completeness, per-chunk digests and cross-file reference checks."""
from __future__ import annotations

import hashlib
from dataclasses import dataclass, field

from evidence.manifest import Manifest
from evidence.receiver import load_current_manifest
from evidence.store import Store


@dataclass
class ValidationError:
    code: str
    detail: str
    file_id: str | None = None
    index: int | None = None


@dataclass
class ValidationReport:
    ok: bool
    errors: list[ValidationError] = field(default_factory=list)


def validate_manifest(store: Store, manifest: Manifest) -> ValidationReport:
    errors: list[ValidationError] = []
    complete_digests: dict[str, str] = {}

    for entry in manifest.files:
        cursor = store.conn.execute(
            "SELECT idx, digest, data FROM chunks WHERE manifest_id=? "
            "AND manifest_version=? AND file_id=? ORDER BY idx",
            (manifest.manifest_id, manifest.version, entry.file_id),
        )
        count_row = store.conn.execute(
            "SELECT COUNT(*) AS n FROM chunks WHERE manifest_id=? "
            "AND manifest_version=? AND file_id=?",
            (manifest.manifest_id, manifest.version, entry.file_id),
        ).fetchone()
        if count_row["n"] != entry.chunk_count:
            errors.append(ValidationError(
                code="INCOMPLETE_FILE",
                file_id=entry.file_id,
                detail=f"have {count_row['n']}/{entry.chunk_count} chunks",
            ))
            continue

        running = hashlib.sha256()
        mismatch: ValidationError | None = None
        for index, row in enumerate(cursor):  # streamed: one chunk resident
            if row["idx"] != index:
                mismatch = ValidationError(
                    code="INCOMPLETE_FILE", file_id=entry.file_id, index=index,
                    detail=f"gap or reorder at chunk index {index}")
                break
            declared = entry.chunk_digests[index]
            if row["digest"] != declared:
                mismatch = ValidationError(
                    code="CHUNK_DIGEST_MISMATCH", file_id=entry.file_id,
                    index=index,
                    detail=f"expected={declared} actual={row['digest']}")
                break
            running.update(row["data"])
        if mismatch is not None:
            errors.append(mismatch)
            continue

        assembled_digest = running.hexdigest()
        if assembled_digest != entry.file_digest:
            errors.append(ValidationError(
                code="FILE_DIGEST_MISMATCH", file_id=entry.file_id,
                detail=f"expected={entry.file_digest} actual={assembled_digest}"))
            continue
        complete_digests[entry.file_id] = assembled_digest

    for ref in manifest.references:
        target = manifest.file_entry(ref.target_file_id)
        if target is None:
            errors.append(ValidationError(
                code="REFERENCE_TARGET_MISSING", file_id=ref.source_file_id,
                detail=f"target {ref.target_file_id} is not declared in manifest"))
            continue
        actual = complete_digests.get(ref.target_file_id)
        if actual is None:
            errors.append(ValidationError(
                code="REFERENCE_TARGET_INCOMPLETE", file_id=ref.source_file_id,
                detail=f"target {ref.target_file_id} did not fully verify"))
        elif actual != ref.target_digest:
            errors.append(ValidationError(
                code="REFERENCE_DIGEST_MISMATCH", file_id=ref.source_file_id,
                detail=f"target {ref.target_file_id}: "
                       f"declared={ref.target_digest} actual={actual}"))

    return ValidationReport(ok=not errors, errors=errors)


def validate_current(store: Store, manifest_id: str) -> ValidationReport:
    return validate_manifest(store, load_current_manifest(store, manifest_id))
