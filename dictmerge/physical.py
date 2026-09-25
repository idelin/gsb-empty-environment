import os
import struct

from .encoding import decode_value, encode_value, tag_type, value_tag
from .spilling import ManagedWriter

LOCAL_MAGIC = b"LDIC01"
LOCAL_INDEX_MAGIC = b"LIDX01"
CHUNK_MAGIC = b"CHNK01"
DICT_MAGIC = b"PDIC01"
REMAP_MAGIC = b"RMAP01"
RECODE_MAGIC = b"RCOD01"

_U8 = struct.Struct("<B")
_U16 = struct.Struct("<H")
_U32 = struct.Struct("<I")
_U64 = struct.Struct("<Q")
_BOOL = struct.Struct("<B")


def _write_text(writer, text):
    data = text.encode("utf-8")
    if len(data) > 0xFFFF:
        raise ValueError("text is too long")
    writer.write(_U16.pack(len(data)))
    writer.write(data)


def _read_text(handle):
    raw = handle.read(_U16.size)
    if len(raw) != _U16.size:
        raise EOFError("truncated text length")
    (length,) = _U16.unpack(raw)
    data = handle.read(length)
    if len(data) != length:
        raise EOFError("truncated text")
    return data.decode("utf-8")


def _write_term(writer, term_id, value_type, value, withdrawn):
    _write_text(writer, term_id)
    writer.write(_BOOL.pack(1 if withdrawn else 0))
    payload = encode_value(value_type, value)
    writer.write(_U8.pack(value_tag(value_type)))
    writer.write(_U32.pack(len(payload)))
    writer.write(payload)


def _read_term(handle, value_type):
    term_id = _read_text(handle)
    raw = handle.read(_BOOL.size)
    if len(raw) != _BOOL.size:
        raise EOFError("truncated withdrawn flag")
    (withdrawn,) = _BOOL.unpack(raw)
    raw = handle.read(_U8.size)
    if len(raw) != _U8.size:
        raise EOFError("truncated value tag")
    (tag,) = _U8.unpack(raw)
    actual_type = tag_type(tag)
    if actual_type != value_type:
        raise ValueError("physical value type does not match column type")
    raw = handle.read(_U32.size)
    if len(raw) != _U32.size:
        raise EOFError("truncated value length")
    (length,) = _U32.unpack(raw)
    payload = handle.read(length)
    if len(payload) != length:
        raise EOFError("truncated value payload")
    from .dictionaries import TermSpec

    return TermSpec(term_id, decode_value(value_type, payload), withdrawn)


def write_local_dictionary(path, batch, budget, ledger, *, buffer_size=4096):
    offsets_by_column = {}
    with ManagedWriter(path, budget, ledger, buffer_size=buffer_size) as writer:
        writer.write(LOCAL_MAGIC)
        versions = {column.column.name: batch.dictionary(column.column.name).version for column in batch.columns}
        versions = {name: version for name, version in versions.items()}
        if len(set(versions.values())) != 1:
            raise ValueError("one batch must use one local dictionary version")
        version = next(iter(versions.values()))
        _write_text(writer, version)
        writer.write(_U32.pack(len(batch.columns)))
        for encoded in batch.columns:
            column = encoded.column
            dictionary = batch.dictionary(column.name)
            if dictionary.version != version:
                raise ValueError("mixed dictionary versions in batch")
            _write_text(writer, column.name)
            _write_text(writer, column.value_type)
            terms = dictionary.terms(column.name)
            writer.write(_U32.pack(len(terms)))
            offsets = []
            for term in terms:
                offsets.append(writer.written_bytes)
                _write_term(writer, term.term_id, column.value_type, term.value, term.withdrawn)
            offsets_by_column[column.name] = (column.value_type, offsets)
    index_path = str(path) + "x"
    with ManagedWriter(index_path, budget, ledger, buffer_size=buffer_size) as writer:
        writer.write(LOCAL_INDEX_MAGIC)
        writer.write(_U32.pack(len(offsets_by_column)))
        for name, (value_type, offsets) in offsets_by_column.items():
            _write_text(writer, name)
            _write_text(writer, value_type)
            writer.write(_U32.pack(len(offsets)))
            for offset in offsets:
                writer.write(_U64.pack(offset))
    return version


class LocalDictionaryReader:
    def __init__(self, path):
        self.path = str(path)
        self.handle = open(self.path, "rb", buffering=0)
        if self.handle.read(len(LOCAL_MAGIC)) != LOCAL_MAGIC:
            raise ValueError("invalid local dictionary file")
        self.version = _read_text(self.handle)
        index_path = self.path + "x"
        self.index_handle = open(index_path, "rb", buffering=0)
        if self.index_handle.read(len(LOCAL_INDEX_MAGIC)) != LOCAL_INDEX_MAGIC:
            raise ValueError("invalid local dictionary index file")
        (column_count,) = _U32.unpack(self._read_index(_U32.size))
        self.columns = []
        self.term_index_offsets = {}
        self.term_counts = {}
        for _ in range(column_count):
            name = _read_text(self.index_handle)
            value_type = _read_text(self.index_handle)
            (count,) = _U32.unpack(self._read_index(_U32.size))
            self.columns.append((name, value_type))
            self.term_counts[name] = count
            self.term_index_offsets[name] = self.index_handle.tell()
            self.index_handle.seek(count * _U64.size, os.SEEK_CUR)
        self.column_map = dict(self.columns)

    def _read_exact(self, length):
        data = self.handle.read(length)
        if len(data) != length:
            raise EOFError("truncated local dictionary")
        return data

    def _read_index(self, length):
        data = self.index_handle.read(length)
        if len(data) != length:
            raise EOFError("truncated local dictionary index")
        return data

    def term(self, column_name, code):
        value_type = self.column_map[column_name]
        if code < 0 or code >= self.term_counts[column_name]:
            raise IndexError(code)
        self.index_handle.seek(self.term_index_offsets[column_name] + code * _U64.size)
        (offset,) = _U64.unpack(self._read_index(_U64.size))
        self.handle.seek(offset)
        return _read_term(self.handle, value_type)

    def close(self):
        self.handle.close()
        self.index_handle.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()


def write_chunk(path, encoded_chunk, column_defs, budget, ledger, *, buffer_size=4096):
    rows = encoded_chunk.row_count
    with ManagedWriter(path, budget, ledger, buffer_size=buffer_size) as writer:
        writer.write(CHUNK_MAGIC)
        writer.write(_U64.pack(encoded_chunk.batch_index))
        writer.write(_U64.pack(encoded_chunk.start_row))
        writer.write(_U64.pack(rows))
        writer.write(_U32.pack(len(column_defs)))
        by_name = {column.column.name: column for column in encoded_chunk.columns}
        for name, value_type in column_defs:
            _write_text(writer, name)
            _write_text(writer, value_type)
            encoded = by_name[name]
            if encoded.column.value_type != value_type:
                raise ValueError("chunk column type mismatch")
            if encoded.row_count != rows:
                raise ValueError("chunk column row count mismatch")
        for row in range(rows):
            for name, _ in column_defs:
                code = by_name[name].codes[row]
                writer.write(_BOOL.pack(0 if code is None else 1))
                if code is not None:
                    writer.write(_U64.pack(code))


class ChunkReader:
    def __init__(self, path):
        self.path = str(path)
        self.handle = open(self.path, "rb", buffering=0)
        if self.handle.read(len(CHUNK_MAGIC)) != CHUNK_MAGIC:
            raise ValueError("invalid chunk file")
        (self.batch_index,) = _U64.unpack(self._read(_U64.size))
        (self.start_row,) = _U64.unpack(self._read(_U64.size))
        (self.row_count,) = _U64.unpack(self._read(_U64.size))
        (column_count,) = _U32.unpack(self._read(_U32.size))
        self.columns = [(_read_text(self.handle), _read_text(self.handle)) for _ in range(column_count)]
        self.rows_read = 0

    def _read(self, length):
        data = self.handle.read(length)
        if len(data) != length:
            raise EOFError("truncated chunk file")
        return data

    def read_row(self):
        if self.rows_read >= self.row_count:
            return None
        values = []
        for _ in self.columns:
            (present,) = _BOOL.unpack(self._read(_BOOL.size))
            if present:
                (code,) = _U64.unpack(self._read(_U64.size))
                values.append(code)
            else:
                values.append(None)
        self.rows_read += 1
        return tuple(values)

    def close(self):
        self.handle.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()


def begin_file(path, magic, budget, ledger, *, category="spill", buffer_size=4096):
    writer = ManagedWriter(path, budget, ledger, buffer_size=buffer_size, category=category)
    writer.write(magic)
    return writer


def patch_u32(path, offset, value):
    with open(path, "r+b", buffering=0) as handle:
        handle.seek(offset)
        handle.write(_U32.pack(value))


def patch_u64(path, offset, value):
    with open(path, "r+b", buffering=0) as handle:
        handle.seek(offset)
        handle.write(_U64.pack(value))


def patch_u32_le(path, offset, value):
    with open(path, "r+b", buffering=0) as handle:
        handle.seek(offset)
        handle.write(struct.pack("<I", value))


def write_published_dictionary(path, columns, winners, budget, ledger, total_rows, *, buffer_size=4096):
    with ManagedWriter(path, budget, ledger, buffer_size=buffer_size, category="dictionary") as writer:
        writer.write(DICT_MAGIC)
        rows_offset = len(DICT_MAGIC)
        writer.write(_U64.pack(total_rows))
        writer.write(_U32.pack(len(columns)))
        for column_name, value_type, entries in columns:
            _write_text(writer, column_name)
            _write_text(writer, value_type)
            writer.write(_U32.pack(len(entries)))
            for code, term_id, value in entries:
                writer.write(_U64.pack(code))
                _write_term(writer, term_id, value_type, value, False)
    return rows_offset


def write_remap(path, *, batch_index, version, column_name, value_type, local_count, pairs, budget, ledger, buffer_size=4096):
    with ManagedWriter(path, budget, ledger, buffer_size=buffer_size, category="remap") as writer:
        writer.write(REMAP_MAGIC)
        writer.write(_U64.pack(batch_index))
        _write_text(writer, version)
        _write_text(writer, column_name)
        _write_text(writer, value_type)
        writer.write(_U64.pack(local_count))
        writer.write(_U64.pack(len(pairs)))
        for local_code, output_code in pairs:
            writer.write(_U64.pack(local_code))
            writer.write(_U64.pack(output_code))


class RemapReader:
    def __init__(self, path):
        self.handle = open(path, "rb", buffering=0)
        if self.handle.read(len(REMAP_MAGIC)) != REMAP_MAGIC:
            raise ValueError("invalid remap file")
        (self.batch_index,) = _U64.unpack(self._read(_U64.size))
        self.version = _read_text(self.handle)
        self.column_name = _read_text(self.handle)
        self.value_type = _read_text(self.handle)
        (self.local_count,) = _U64.unpack(self._read(_U64.size))
        (self.mapping_count,) = _U64.unpack(self._read(_U64.size))
        self.read_count = 0

    def _read(self, length):
        data = self.handle.read(length)
        if len(data) != length:
            raise EOFError("truncated remap")
        return data

    def read_pair(self):
        if self.read_count >= self.mapping_count:
            return None
        pair = _U64.unpack(self._read(_U64.size))[0], _U64.unpack(self._read(_U64.size))[0]
        self.read_count += 1
        return pair

    def close(self):
        self.handle.close()


def write_recoded_from_streams(path, columns, row_count, bitmap_paths, code_paths, non_null_counts, budget, ledger, *, buffer_size=4096):
    with ManagedWriter(path, budget, ledger, buffer_size=buffer_size, category="recoded") as writer:
        writer.write(RECODE_MAGIC)
        writer.write(_U64.pack(row_count))
        writer.write(_U32.pack(len(columns)))
        bitmap_size = (row_count + 7) // 8
        for (column_name, value_type), bitmap_path, code_path, non_null in zip(columns, bitmap_paths, code_paths, non_null_counts):
            _write_text(writer, column_name)
            _write_text(writer, value_type)
            writer.write(_U64.pack(non_null))
            with open(bitmap_path, "rb", buffering=0) as bitmap:
                data = bitmap.read()
                if len(data) != bitmap_size:
                    raise EOFError("invalid temporary bitmap size")
                writer.write(data)
            with open(code_path, "rb", buffering=0) as codes:
                while True:
                    data = codes.read(4096)
                    if not data:
                        break
                    writer.write(data)


class RecodedReader:
    def __init__(self, path):
        self.handle = open(path, "rb", buffering=0)
        if self.handle.read(len(RECODE_MAGIC)) != RECODE_MAGIC:
            raise ValueError("invalid recoded batch")
        (self.row_count,) = _U64.unpack(self._read(_U64.size))
        (column_count,) = _U32.unpack(self._read(_U32.size))
        self.columns = []
        self.bitmaps = []
        self.code_offsets = []
        bitmap_size = (self.row_count + 7) // 8
        self.non_null_counts = []
        for _ in range(column_count):
            name = _read_text(self.handle)
            value_type = _read_text(self.handle)
            (non_null,) = _U64.unpack(self._read(_U64.size))
            bitmap = self._read(bitmap_size)
            self.non_null_counts.append(non_null)
            self.columns.append((name, value_type))
            self.bitmaps.append(bitmap)
            code_offset = self.handle.tell()
            self.handle.seek(non_null * _U64.size, os.SEEK_CUR)
            self.code_offsets.append(code_offset)

    def _read(self, length):
        data = self.handle.read(length)
        if len(data) != length:
            raise EOFError("truncated recoded batch")
        return data

    def decode(self, published_dictionary):
        result = {}
        for column_index, (column_name, value_type) in enumerate(self.columns):
            terms = published_dictionary[column_name]
            self.handle.seek(self.code_offsets[column_index])
            values = []
            bitmap = self.bitmaps[column_index]
            for row in range(self.row_count):
                if not ((bitmap[row // 8] >> (row % 8)) & 1):
                    values.append(None)
                else:
                    (code,) = _U64.unpack(self._read(_U64.size))
                    values.append(terms[code].value)
            result[column_name] = tuple(values)
        return result

    def close(self):
        self.handle.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()


class PublishedDictionaryReader:
    def __init__(self, path):
        self.path = str(path)
        self.handle = open(self.path, "rb", buffering=0)
        if self.handle.read(len(DICT_MAGIC)) != DICT_MAGIC:
            raise ValueError("invalid published dictionary file")
        (self.row_count,) = _U64.unpack(self._read(_U64.size))
        (column_count,) = _U32.unpack(self._read(_U32.size))
        self.columns = []
        self.term_offsets = {}
        self.term_codes = {}
        for _ in range(column_count):
            name = _read_text(self.handle)
            value_type = _read_text(self.handle)
            self.columns.append((name, value_type))
            self.term_offsets[name] = []
            self.term_codes[name] = []
            (count,) = _U32.unpack(self._read(_U32.size))
            for _ in range(count):
                (code,) = _U64.unpack(self._read(_U64.size))
                self.term_offsets[name].append(self.handle.tell())
                self.term_codes[name].append(code)
                _read_term(self.handle, value_type)

    def _read(self, length):
        data = self.handle.read(length)
        if len(data) != length:
            raise EOFError("truncated published dictionary")
        return data

    def term(self, column_name, code):
        value_type = dict(self.columns)[column_name]
        offsets = self.term_offsets[column_name]
        if code < 0 or code >= len(offsets):
            raise IndexError(code)
        self.handle.seek(offsets[code])
        return _read_term(self.handle, value_type)

    def as_decode_map(self):
        class _Terms:
            def __init__(inner, reader, column_name):
                inner.reader = reader
                inner.column_name = column_name

            def __getitem__(inner, code):
                return inner.reader.term(inner.column_name, code)

        return {name: _Terms(self, name) for name, _ in self.columns}

    def close(self):
        self.handle.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()


def begin_remap(path, *, batch_index, version, column_name, value_type, local_count, budget, ledger, buffer_size=1):
    writer = ManagedWriter(path, budget, ledger, buffer_size=buffer_size, category="remap")
    writer.write(REMAP_MAGIC)
    writer.write(_U64.pack(batch_index))
    _write_text(writer, version)
    _write_text(writer, column_name)
    _write_text(writer, value_type)
    writer.write(_U64.pack(local_count))
    count_offset = writer.written_bytes
    writer.write(_U64.pack(0))
    return writer, count_offset


def finish_remap(writer, count, count_offset):
    writer.write(b"")
    writer.close()
    with open(writer.path, "r+b", buffering=0) as handle:
        handle.seek(count_offset)
        handle.write(_U64.pack(count))
