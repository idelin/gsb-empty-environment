import hashlib
import json
import os
import shutil
import struct
import tempfile
import uuid
from dataclasses import dataclass

from .encoding import decode_value, encode_value, values_equal
from .errors import (
    ChunkCoverageError,
    InvalidDictionaryCodeError,
    MergeCancelled,
    SchemaError,
    StableTermConflictError,
    WithdrawnTermError,
)
from .physical import (
    ChunkReader,
    LocalDictionaryReader,
    PublishedDictionaryReader,
    RecodedReader,
    begin_file,
    begin_remap,
    write_chunk,
    write_local_dictionary,
    write_published_dictionary,
    write_recoded_from_streams,
    finish_remap,
    write_remap,
    _write_text as physical_write_text,
)
from .sorter import ExternalSorter
from .spilling import (
    AccountedBytes,
    CancellationToken,
    Ledger,
    MemoryBudget,
    ManagedWriter,
    atomic_replace,
    atomic_write_bytes,
    ensure_dir,
    fsync_dir,
)

MANIFEST_NAME = "manifest.json"
CHUNKS_DIR = "chunks"
LOCAL_DICTS_DIR = "local-dictionaries"
WORK_DIR = "work"
PUBLISH_DIR = "published"
GENERATION = "generation-0001"
DICTIONARY_FILE = "dictionary.pdic"
RECODED_FILE = "batch.rcod"

_U16 = struct.Struct(">H")
_U32 = struct.Struct(">I")
_U64 = struct.Struct(">Q")
_I64 = struct.Struct(">q")
_EVENT_KEY = struct.Struct(">IIQ")
_EVENT_PAYLOAD = struct.Struct(">QQ")
_CANDIDATE_PAYLOAD = struct.Struct(">Qqq")
_MEMBER_PAYLOAD = struct.Struct(">IIQ")
_MAP_KEY = struct.Struct(">IIQ")
_MAP_PAYLOAD = struct.Struct(">Q")
_OUTPUT_WINNER_PAYLOAD = struct.Struct(">Q")
_TERM_WINNER_PAYLOAD = struct.Struct(">Q")
_RECODE_KEY = struct.Struct(">QQ")
_RECODE_PAYLOAD = struct.Struct(">IIQ")


@dataclass(frozen=True)
class MergeResult:
    workdir: str
    dictionary_path: str
    recoded_batch_path: str
    generation: str
    row_count: int
    accounting: AccountedBytes
    sha256: dict[str, str]

    def decode(self):
        with PublishedDictionaryReader(self.dictionary_path) as dictionary:
            with RecodedReader(self.recoded_batch_path) as batch:
                return batch.decode(dictionary.as_decode_map())

    def accounting_dict(self):
        return self.accounting.as_dict()


def _u16(value):
    if value < 0 or value > 0xFFFF:
        raise ValueError("u16 out of range")
    return _U16.pack(value)


def _u32(value):
    if value < 0 or value > 0xFFFFFFFF:
        raise ValueError("u32 out of range")
    return _U32.pack(value)


def _u64(value):
    if value < 0 or value > 0xFFFFFFFFFFFFFFFF:
        raise ValueError("u64 out of range")
    return _U64.pack(value)


def _i64(value):
    if value < -0x8000000000000000 or value > 0x7FFFFFFFFFFFFFFF:
        raise ValueError("i64 out of range")
    return _I64.pack(value)


def _term_key(column_index, term_id):
    raw = term_id.encode("utf-8")
    return _u32(column_index) + _u16(len(raw)) + raw


def _safe_name(value):
    return "".join(ch if ch.isalnum() or ch in "._-" else "_" for ch in value)


class MergeJob:
    def __init__(
        self,
        workdir,
        batches,
        *,
        memory_limit_bytes=1 << 24,
        buffer_size=4096,
        job_id=None,
    ):
        self.workdir = os.path.abspath(str(workdir))
        self.io_buffer_size = 1
        self.manifest_path = os.path.join(self.workdir, MANIFEST_NAME)
        if os.path.exists(self.manifest_path):
            raise FileExistsError(f"merge job already exists: {self.manifest_path}")
        if os.path.isdir(self.workdir) and os.listdir(self.workdir):
            raise FileExistsError("workdir must be empty for a new merge job")
        self.batches = tuple(batches)
        self._validate_batches()
        self.job_id = job_id or uuid.uuid4().hex
        self.memory_limit_bytes = int(memory_limit_bytes)
        self.chunk_counter = 0
        self.cancelled = False
        self.state = "receiving"
        self.ledger = Ledger(AccountedBytes(memory_limit_bytes=self.memory_limit_bytes))
        self.accounting = self.ledger.accounting
        self.received_ranges = {batch.batch_index: [] for batch in self.batches}
        self.budget = MemoryBudget(self.memory_limit_bytes)
        ensure_dir(os.path.join(self.workdir, CHUNKS_DIR))
        ensure_dir(os.path.join(self.workdir, LOCAL_DICTS_DIR))
        ensure_dir(os.path.join(self.workdir, WORK_DIR))
        ensure_dir(os.path.join(self.workdir, PUBLISH_DIR))
        self._persist_local_dictionaries()
        self._save_manifest()

    @classmethod
    def resume(cls, workdir):
        workdir = os.path.abspath(str(workdir))
        manifest_path = os.path.join(workdir, MANIFEST_NAME)
        with open(manifest_path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        object_ = cls.__new__(cls)
        object_.workdir = workdir
        object_.manifest_path = manifest_path
        object_.job_id = data["job_id"]
        object_.memory_limit_bytes = data["memory_limit_bytes"]
        object_.io_buffer_size = data.get("io_buffer_size", 4096)
        object_.batches = tuple(batch_index for batch_index in data["batch_indices"])
        object_.schema = [(item["name"], item["type"]) for item in data["schema"]]
        object_.batch_versions = {int(key): value for key, value in data["batch_versions"].items()}
        object_.batch_row_counts = {int(key): value for key, value in data["batch_row_counts"].items()}
        object_.chunk_counter = data.get("chunk_counter", 0)
        object_.cancelled = data.get("cancelled", False)
        object_.state = "receiving" if data.get("state") == "cancelled" else data.get("state", "receiving")
        accounting_data = data.get("accounting", {})
        object_.accounting = AccountedBytes(**{
            key: accounting_data.get(key, 0)
            for key in AccountedBytes().__dict__
        })
        object_.ledger = Ledger(object_.accounting)
        object_.received_ranges = {}
        for key, ranges in data.get("received_ranges", {}).items():
            object_.received_ranges[int(key)] = [tuple(item) for item in ranges]
        object_.budget = MemoryBudget(object_.memory_limit_bytes, peak_bytes=object_.accounting.peak_memory_bytes)
        if object_.cancelled:
            object_.cancelled = False
            object_._save_manifest()
        return object_

    def _validate_batches(self):
        if not self.batches:
            raise SchemaError("at least one input batch is required")
        indices = [batch.batch_index for batch in self.batches]
        if indices != list(range(len(self.batches))):
            raise SchemaError("batch indices must be dense and ordered 0..N-1")
        schema = [(column.column.name, column.column.value_type) for column in self.batches[0].columns]
        for batch in self.batches:
            current = [(column.column.name, column.column.value_type) for column in batch.columns]
            if current != schema:
                raise SchemaError("all batches must use identical typed column schema")
            for name, value_type in schema:
                column = next(column for column in batch.columns if column.column.name == name)
                terms = batch.dictionary(name).terms(name)
                for term in terms:
                    if term.term_id is None:
                        raise SchemaError("term id cannot be None")
        self.schema = schema
        self.batch_row_counts = {batch.batch_index: batch.row_count for batch in self.batches}
        self.batch_versions = {}
        for batch in self.batches:
            versions = {batch.dictionary(name).version for name, _ in self.schema}
            if len(versions) != 1:
                raise SchemaError(f"batch {batch.batch_index} mixes dictionary versions")
            self.batch_versions[batch.batch_index] = next(iter(versions))

    def _persist_local_dictionaries(self):
        for batch in self.batches:
            path = self.local_dictionary_path(batch.batch_index)
            ensure_dir(os.path.dirname(path))
            if not os.path.exists(path):
                write_local_dictionary(
                    path,
                    batch,
                    self.budget,
                    self.ledger,
                    buffer_size=self.io_buffer_size,
                )

    @property
    def total_rows(self):
        return sum(self.batch_row_counts.values())

    def local_dictionary_path(self, batch_index):
        return os.path.join(
            self.workdir,
            LOCAL_DICTS_DIR,
            f"batch-{batch_index:06d}.ldic",
        )

    def chunk_path(self, sequence):
        return os.path.join(self.workdir, CHUNKS_DIR, f"chunk-{sequence:08d}.chnk")

    def _manifest_dict(self):
        batch_indices = [
            batch.batch_index if hasattr(batch, "batch_index") else int(batch)
            for batch in self.batches
        ]
        return {
            "job_id": self.job_id,
            "schema": [{"name": name, "type": value_type} for name, value_type in self.schema],
            "batch_indices": batch_indices,
            "batch_row_counts": self.batch_row_counts,
            "batch_versions": self.batch_versions,
            "memory_limit_bytes": self.memory_limit_bytes,
            "io_buffer_size": self.io_buffer_size,
            "chunk_counter": self.chunk_counter,
            "received_ranges": {
                str(batch_index): [list(item) for item in ranges]
                for batch_index, ranges in self.received_ranges.items()
            },
            "state": self.state,
            "cancelled": self.cancelled,
            "accounting": self.accounting.as_dict(),
        }

    def _save_manifest(self):
        data = json.dumps(self._manifest_dict(), sort_keys=True, indent=2).encode("utf-8")
        atomic_write_bytes(self.manifest_path, data)

    def add_chunk(self, encoded_chunk, *, cancellation_token=None):
        self._active_check(cancellation_token)
        batch_index = encoded_chunk.batch_index
        if batch_index not in self.batch_row_counts:
            raise ChunkCoverageError(f"unknown batch {batch_index}")
        start = encoded_chunk.start_row
        end = start + encoded_chunk.row_count
        if start < 0 or end > self.batch_row_counts[batch_index]:
            raise ChunkCoverageError("chunk range is outside its batch")
        names = [(column.column.name, column.column.value_type) for column in encoded_chunk.columns]
        if names != self.schema:
            raise SchemaError("chunk schema does not match job schema")
        self._check_overlap(batch_index, start, end)
        sequence = self.chunk_counter
        path = self.chunk_path(sequence)
        write_chunk(
            path,
            encoded_chunk,
            self.schema,
            self.budget,
            self._live_ledger(),
            buffer_size=self.io_buffer_size,
        )
        self.chunk_counter += 1
        self.received_ranges[batch_index].append((start, end))
        self._refresh_peak()
        self._save_manifest()
        self._active_check(cancellation_token)
        return path

    def _live_ledger(self):
        return self.ledger

    def _refresh_peak(self):
        self.ledger.accounting.peak_memory_bytes = max(
            self.ledger.accounting.peak_memory_bytes,
            self.budget.peak_bytes,
        )
        self.accounting.spill_bytes_written += self.budget.used_bytes * 0

    def cancel(self):
        self.cancelled = True
        self.state = "cancelled"
        self._save_manifest()

    def _active_check(self, cancellation_token):
        if self.cancelled or (cancellation_token is not None and cancellation_token.is_cancelled()):
            self.cancelled = True
            self.state = "cancelled"
            self._save_manifest()
            raise MergeCancelled("merge job is cancelled")

    def _check_overlap(self, batch_index, start, end):
        if start == end:
            return
        for old_start, old_end in self.received_ranges[batch_index]:
            if start < old_end and old_start < end:
                raise ChunkCoverageError(
                    f"overlapping chunk in batch {batch_index}: [{start},{end}) vs [{old_start},{old_end})"
                )

    def validate_coverage(self):
        for batch_index, row_count in self.batch_row_counts.items():
            ranges = sorted(self.received_ranges.get(batch_index, []))
            cursor = 0
            for start, end in ranges:
                if start != cursor:
                    raise ChunkCoverageError(
                        f"batch {batch_index} has a gap at row {cursor + 1}; chunk starts at {start + 1}"
                    )
                cursor = end
            if cursor != row_count:
                raise ChunkCoverageError(
                    f"batch {batch_index} is incomplete: {cursor} of {row_count} rows received"
                )

    def _chunk_order(self):
        paths = []
        directory = os.path.join(self.workdir, CHUNKS_DIR)
        for name in os.listdir(directory):
            if name.endswith(".chnk"):
                paths.append(os.path.join(directory, name))
        ordered = []
        for path in sorted(paths):
            with ChunkReader(path) as reader:
                ordered.append((reader.batch_index, reader.start_row, path))
        return sorted(ordered)

    def _published_paths(self):
        generation_dir = os.path.join(self.workdir, PUBLISH_DIR, GENERATION)
        return (
            generation_dir,
            os.path.join(generation_dir, DICTIONARY_FILE),
            os.path.join(generation_dir, RECODED_FILE),
            os.path.join(generation_dir, "remaps"),
        )

    def _existing_result(self):
        generation_dir, dictionary_path, recoded_path, remap_dir = self._published_paths()
        manifest_path = os.path.join(generation_dir, "release.json")
        if not os.path.isdir(generation_dir) or not os.path.exists(manifest_path):
            return None
        with open(manifest_path, "r", encoding="utf-8") as handle:
            release = json.load(handle)
        return MergeResult(
            workdir=self.workdir,
            dictionary_path=dictionary_path,
            recoded_batch_path=recoded_path,
            generation=GENERATION,
            row_count=release["row_count"],
            accounting=AccountedBytes(**release["accounting"]),
            sha256=release["sha256"],
        )

    def finalize(self, cancellation_token=None):
        existing = self._existing_result()
        if existing is not None:
            return existing
        self.validate_coverage()
        try:
            return self._run_finalize(cancellation_token)
        except MergeCancelled:
            self.cancelled = True
            self.state = "cancelled"
            self._save_manifest()
            raise

    def _new_attempt(self):
        publish_root = os.path.join(self.workdir, PUBLISH_DIR)
        ensure_dir(publish_root)
        for name in os.listdir(publish_root):
            if name.startswith(".txn-"):
                shutil.rmtree(os.path.join(publish_root, name), ignore_errors=True)
        work_root = os.path.join(self.workdir, WORK_DIR)
        for name in os.listdir(work_root):
            if name.startswith("attempt-"):
                stale = os.path.join(work_root, name)
                for root, _, files in os.walk(stale):
                    for filename in files:
                        stale_path = os.path.join(root, filename)
                        self.ledger.add_delete(os.path.getsize(stale_path))
                shutil.rmtree(stale, ignore_errors=True)
        attempt = tempfile.mkdtemp(prefix="attempt-", dir=work_root)
        txn = tempfile.mkdtemp(prefix=".txn-", dir=publish_root)
        ensure_dir(os.path.join(txn, "remaps"))
        return attempt, txn

    def _run_finalize(self, cancellation_token):
        self._active_check(cancellation_token)
        self.state = "finalizing"
        self._save_manifest()
        batch_offsets = {}
        cursor = 0
        for batch_index in sorted(self.batch_row_counts):
            batch_offsets[batch_index] = cursor
            cursor += self.batch_row_counts[batch_index]
        total_rows = cursor
        self.ledger.accounting.physical_dictionary_bytes = 0
        self.ledger.accounting.physical_remap_bytes = 0
        self.ledger.accounting.physical_recoded_bytes = 0
        attempt, txn = self._new_attempt()
        local_readers = {}

        def local_reader(batch_index):
            if batch_index not in local_readers:
                local_readers[batch_index] = LocalDictionaryReader(self.local_dictionary_path(batch_index))
            return local_readers[batch_index]

        try:
            raw_dir = os.path.join(attempt, "raw")
            candidate_dir = os.path.join(attempt, "candidates")
            output_dir = os.path.join(attempt, "output-winners")
            term_dir = os.path.join(attempt, "term-output")
            map_dir = os.path.join(attempt, "source-map")
            recode_dir = os.path.join(attempt, "recode-rows")
            for path in (raw_dir, candidate_dir, output_dir, term_dir, map_dir, recode_dir):
                ensure_dir(path)

            raw_sorter = ExternalSorter(
                raw_dir, self.budget, self.ledger, key_length=_EVENT_KEY.size,
                run_prefix="raw-", buffer_size=1,
            )
            for batch_index, start_row, chunk_path in self._chunk_order():
                reader = local_reader(batch_index)
                term_counts = reader.term_counts
                with ChunkReader(chunk_path) as chunk:
                    for local_row, codes in enumerate(iter(chunk.read_row, None)):
                        self._check_cancel(cancellation_token, raw_sorter, local_readers)
                        global_row = batch_offsets[batch_index] + start_row + local_row
                        for column_index, code in enumerate(codes):
                            column_name = self.schema[column_index][0]
                            if code is None:
                                continue
                            if code >= term_counts[column_name]:
                                raise InvalidDictionaryCodeError(
                                    batch_index=batch_index,
                                    column=column_name,
                                    code=code,
                                    batch_row=start_row + local_row + 1,
                                    global_row=global_row + 1,
                                    version=self.batch_versions[batch_index],
                                )
                            raw_sorter.add_record(
                                _EVENT_KEY.pack(column_index, batch_index, code),
                                _EVENT_PAYLOAD.pack(global_row, start_row + local_row),
                            )
            raw_sorter.flush(cancellation_token)
            self._refresh_peak()

            candidate_sorter = ExternalSorter(
                candidate_dir, self.budget, self.ledger, key_length=0,
                run_prefix="candidate-", buffer_size=1,
            )
            for group in self._iter_groups(raw_sorter.iter_merged(cancellation_token)):
                key, payload = group[0]
                column_index, batch_index, code = _EVENT_KEY.unpack(key)
                first_global, first_batch_row = _EVENT_PAYLOAD.unpack(payload)
                dictionary = local_reader(batch_index)
                column_name = self.schema[column_index][0]
                term = dictionary.term(column_name, code)
                if term.withdrawn:
                    raise WithdrawnTermError(
                        batch_index=batch_index,
                        column=column_name,
                        code=code,
                        batch_row=first_batch_row + 1,
                        global_row=first_global + 1,
                        version=self.batch_versions[batch_index],
                        term_id=term.term_id,
                    )
                candidate_sorter.add_record(
                    _term_key(column_index, term.term_id),
                    _CANDIDATE_PAYLOAD.pack(first_global, batch_index, code),
                )
            candidate_sorter.flush(cancellation_token)
            self._refresh_peak()

            output_winner_sorter = ExternalSorter(
                output_dir, self.budget, self.ledger, key_length=_MAP_KEY.size,
                run_prefix="output-", buffer_size=1,
            )
            term_winner_sorter = ExternalSorter(
                term_dir, self.budget, self.ledger, key_length=0,
                run_prefix="term-winner-", buffer_size=1,
            )
            for records in self._iter_groups(candidate_sorter.iter_merged(cancellation_token)):
                column_index = _U32.unpack(records[0][0][:4])[0]
                term_id = self._key_term_id(records[0][0])
                value_type = self.schema[column_index][1]
                first_payload = _CANDIDATE_PAYLOAD.unpack(records[0][1])
                first_global, winner_batch, winner_code = first_payload
                winner_term = local_reader(winner_batch).term(self.schema[column_index][0], winner_code)
                for _, payload in records[1:]:
                    _, other_batch, other_code = _CANDIDATE_PAYLOAD.unpack(payload)
                    other_term = local_reader(other_batch).term(self.schema[column_index][0], other_code)
                    if not values_equal(value_type, winner_term.value, other_term.value):
                        raise StableTermConflictError(
                            f"stable term {term_id!r} has conflicting values in column {self.schema[column_index][0]!r}"
                        )
                output_winner_sorter.add_record(
                    _MAP_KEY.pack(column_index, winner_batch, winner_code),
                    _OUTPUT_WINNER_PAYLOAD.pack(first_global),
                )
                term_winner_sorter.add_record(
                    _term_key(column_index, term_id),
                    _TERM_WINNER_PAYLOAD.pack(first_global),
                )
            output_winner_sorter.flush(cancellation_token)
            term_winner_sorter.flush(cancellation_token)
            self._refresh_peak()

            part_paths = {}
            part_counts = {}
            current_column = None
            part_writer = None
            term_output_sorter = ExternalSorter(
                os.path.join(attempt, "term-output-codes"), self.budget, self.ledger, key_length=0,
                run_prefix="term-code-", buffer_size=1,
            )
            output_code_by_column = {}
            for key, payload in output_winner_sorter.iter_merged(cancellation_token):
                column_index, winner_batch, winner_code = _MAP_KEY.unpack(key)
                if current_column != column_index:
                    if part_writer is not None:
                        part_writer.close()
                        from .physical import patch_u32_le
                        patch_u32_le(part_paths[current_column], 4, part_counts[current_column])
                    current_column = column_index
                    output_code_by_column[column_index] = 0
                    part_path = os.path.join(attempt, f"dict-part-{column_index:04d}.dcp")
                    part_paths[column_index] = part_path
                    part_counts[column_index] = 0
                    part_writer = ManagedWriter(
                        part_path, self.budget, self.ledger,
                        buffer_size=self.io_buffer_size, category="spill",
                    )
                    part_writer.write(b"DCP0")
                    part_writer.write(struct.pack("<I", 0))
                output_code = output_code_by_column[column_index]
                output_code_by_column[column_index] = output_code + 1
                part_counts[column_index] += 1
                column_name, value_type = self.schema[column_index]
                term = local_reader(winner_batch).term(column_name, winner_code)
                part_writer.write(struct.pack("<Q", output_code))
                from .physical import _write_term
                _write_term(part_writer, term.term_id, value_type, term.value, False)
                term_output_sorter.add_record(
                    _term_key(column_index, term.term_id),
                    _U64.pack(output_code),
                )
            if part_writer is not None:
                part_writer.close()
                from .physical import patch_u32_le
                patch_u32_le(part_paths[current_column], 4, part_counts[current_column])
            term_output_sorter.flush(cancellation_token)

            source_map_sorter = ExternalSorter(
                map_dir, self.budget, self.ledger, key_length=_MAP_KEY.size,
                run_prefix="map-", buffer_size=1,
            )
            term_outputs = term_output_sorter.iter_merged(cancellation_token)
            output_pair = next(term_outputs, None)
            for records in self._iter_groups(candidate_sorter.iter_merged(cancellation_token)):
                term_key = records[0][0]
                while output_pair is not None and output_pair[0] < term_key:
                    output_pair = next(term_outputs, None)
                if output_pair is None or output_pair[0] != term_key:
                    raise StableTermConflictError("missing output code for stable term")
                output_code = _U64.unpack(output_pair[1])[0]
                for _, payload in records:
                    _, batch_index, code = _CANDIDATE_PAYLOAD.unpack(payload)
                    column_index = _U32.unpack(term_key[:4])[0]
                    source_map_sorter.add_record(
                        _MAP_KEY.pack(column_index, batch_index, code),
                        _MAP_PAYLOAD.pack(output_code),
                    )
            source_map_sorter.flush(cancellation_token)

            remap_dir = os.path.join(txn, "remaps")
            current_group = None
            remap_writer = None
            remap_count_offset = 0
            remap_count = 0
            map_iter = source_map_sorter.iter_merged(cancellation_token)

            def close_remap():
                nonlocal current_group, remap_writer, remap_count_offset, remap_count
                if current_group is None:
                    return
                finish_remap(remap_writer, remap_count, remap_count_offset)
                current_group = None
                remap_writer = None
                remap_count_offset = 0
                remap_count = 0

            for key, payload in map_iter:
                column_index, batch_index, code = _MAP_KEY.unpack(key)
                group = (column_index, batch_index)
                if current_group != group:
                    close_remap()
                    current_group = group
                    column_name, value_type = self.schema[column_index]
                    local_count = local_reader(batch_index).term_counts[column_name]
                    safe_column = _safe_name(column_name)
                    remap_path = os.path.join(
                        remap_dir,
                        f"batch-{batch_index:06d}-column-{column_index:04d}-{safe_column}.rmap",
                    )
                    remap_writer, remap_count_offset = begin_remap(
                        remap_path,
                        batch_index=batch_index,
                        version=self.batch_versions[batch_index],
                        column_name=column_name,
                        value_type=value_type,
                        local_count=local_count,
                        budget=self.budget,
                        ledger=self.ledger,
                        buffer_size=1,
                    )
                    remap_count = 0
                remap_writer.write(_U64.pack(code))
                remap_writer.write(payload)
                remap_count += 1
            close_remap()
            recode_sorter = ExternalSorter(
                recode_dir, self.budget, self.ledger, key_length=_RECODE_KEY.size,
                run_prefix="recode-", buffer_size=1,
            )
            current_group = None
            mappings = iter(source_map_sorter.iter_merged(cancellation_token))
            mapping_pair = next(mappings, None)

            def mapping_for(column_index, batch_index, code):
                nonlocal mapping_pair
                wanted = _MAP_KEY.pack(column_index, batch_index, code)
                while mapping_pair is not None and mapping_pair[0] < wanted:
                    mapping_pair = next(mappings, None)
                if mapping_pair is None or mapping_pair[0] != wanted:
                    raise StableTermConflictError("missing remap entry during recoding")
                return _U64.unpack(mapping_pair[1])[0]

            for key, payload in raw_sorter.iter_merged(cancellation_token):
                column_index, batch_index, code = _EVENT_KEY.unpack(key)
                first_global, first_batch_row = _EVENT_PAYLOAD.unpack(payload)
                output_code = mapping_for(column_index, batch_index, code)
                recode_sorter.add_record(
                    _RECODE_KEY.pack(column_index, first_global),
                    _RECODE_PAYLOAD.pack(batch_index, code, output_code),
                )
            recode_sorter.flush(cancellation_token)

            bitmap_paths = []
            code_paths = []
            non_null_counts = []
            for column_index, (column_name, value_type) in enumerate(self.schema):
                bitmap_path = os.path.join(attempt, f"bitmap-{column_index:04d}.bits")
                code_path = os.path.join(attempt, f"codes-{column_index:04d}.u64")
                bitmap_paths.append(bitmap_path)
                code_paths.append(code_path)
                non_null_counts.append(0)
                with open(bitmap_path, "wb", buffering=0) as bitmap:
                    bitmap.truncate((total_rows + 7) // 8)
                bitmap = open(bitmap_path, "r+b", buffering=0)
                code_writer = ManagedWriter(
                    code_path, self.budget, self.ledger,
                    buffer_size=self.io_buffer_size, category="spill",
                )
                try:
                    for key, payload in recode_sorter.iter_merged(cancellation_token):
                        record_column_index = _U64.unpack(key[:8])[0]
                        if record_column_index < column_index:
                            continue
                        if record_column_index > column_index:
                            break
                        global_row = _U64.unpack(key[8:])[0]
                        _, _, output_code = _RECODE_PAYLOAD.unpack(payload)
                        byte_index = global_row // 8
                        bitmap.seek(byte_index)
                        raw = bitmap.read(1) or b"\x00"
                        new_value = raw[0] | (1 << (global_row % 8))
                        bitmap.seek(byte_index)
                        bitmap.write(bytes([new_value]))
                        code_writer.write(struct.pack("<Q", output_code))
                        non_null_counts[column_index] += 1
                finally:
                    bitmap.close()
                    code_writer.close()

            recoded_tmp = os.path.join(attempt, "batch.rcod")
            write_recoded_from_streams(
                recoded_tmp,
                self.schema,
                total_rows,
                bitmap_paths,
                code_paths,
                non_null_counts,
                self.budget,
                self.ledger,
                buffer_size=self.io_buffer_size,
            )

            dictionary_tmp = os.path.join(attempt, DICTIONARY_FILE)
            with ManagedWriter(
                dictionary_tmp, self.budget, self.ledger,
                buffer_size=self.io_buffer_size, category="dictionary",
            ) as dictionary_writer:
                dictionary_writer.write(b"PDIC01")
                dictionary_writer.write(struct.pack("<Q", total_rows))
                dictionary_writer.write(struct.pack("<I", len(self.schema)))
                for column_index, (column_name, value_type) in enumerate(self.schema):
                    part_path = part_paths.get(column_index)
                    count = 0
                    physical_write_text(dictionary_writer, column_name)
                    physical_write_text(dictionary_writer, value_type)
                    if part_path is None:
                        dictionary_writer.write(struct.pack("<I", 0))
                        continue
                    with open(part_path, "rb", buffering=0) as part:
                        if part.read(4) != b"DCP0":
                            raise ValueError("invalid dictionary part")
                        raw_count = part.read(4)
                        (count,) = struct.unpack("<I", raw_count)
                        dictionary_writer.write(raw_count)
                        while True:
                            block = part.read(self.io_buffer_size)
                            if not block:
                                break
                            dictionary_writer.write(block)

            txn_dictionary = os.path.join(txn, DICTIONARY_FILE)
            txn_recoded = os.path.join(txn, RECODED_FILE)
            os.replace(dictionary_tmp, txn_dictionary)
            os.replace(recoded_tmp, txn_recoded)
            hashes = {
                DICTIONARY_FILE: self._sha256(txn_dictionary),
                RECODED_FILE: self._sha256(txn_recoded),
            }
            release = {
                "generation": GENERATION,
                "row_count": total_rows,
                "sha256": hashes,
                "accounting": self.ledger.accounting.as_dict(),
            }
            atomic_write_bytes(os.path.join(txn, "release.json"), json.dumps(release, sort_keys=True, indent=2).encode("utf-8"))
            final_generation = os.path.join(self.workdir, PUBLISH_DIR, GENERATION)
            if os.path.exists(final_generation):
                raise FileExistsError(f"published generation already exists: {GENERATION}")
            fsync_dir(txn)
            atomic_replace(txn, final_generation)
            fsync_dir(os.path.join(self.workdir, PUBLISH_DIR))

            self._cleanup_sorters([
                raw_sorter,
                candidate_sorter,
                output_winner_sorter,
                term_winner_sorter,
                term_output_sorter,
                source_map_sorter,
                recode_sorter,
            ])
            shutil.rmtree(attempt, ignore_errors=True)
            self.state = "published"
            self._refresh_peak()
            self._save_manifest()
            return MergeResult(
                workdir=self.workdir,
                dictionary_path=os.path.join(final_generation, DICTIONARY_FILE),
                recoded_batch_path=os.path.join(final_generation, RECODED_FILE),
                generation=GENERATION,
                row_count=total_rows,
                accounting=self.ledger.accounting,
                sha256=hashes,
            )
        finally:
            for reader in local_readers.values():
                reader.close()

    @staticmethod
    def _iter_groups(record_iterator):
        group = []
        current_key = None
        for key, payload in record_iterator:
            if current_key is None or key == current_key:
                group.append((key, payload))
            else:
                yield group
                group = [(key, payload)]
            current_key = key
        if group:
            yield group

    @staticmethod
    def _key_term_id(key):
        length = _U16.unpack(key[4:6])[0]
        return key[6:6 + length].decode("utf-8")

    def _check_cancel(self, cancellation_token, *closeable):
        if cancellation_token is not None and cancellation_token.is_cancelled():
            for item in closeable:
                if hasattr(item, "flush"):
                    item.flush(None)
            raise MergeCancelled("merge cancelled while scanning chunks")

    @staticmethod
    def _cleanup_sorters(sorters):
        for sorter in sorters:
            sorter.cleanup()

    @staticmethod
    def _sha256(path):
        digest = hashlib.sha256()
        with open(path, "rb") as handle:
            for block in iter(lambda: handle.read(65536), b""):
                digest.update(block)
        return digest.hexdigest()
