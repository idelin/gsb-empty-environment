import base64
import json
import os
import tempfile
import zlib
from collections.abc import Iterator

from .errors import CheckpointChecksumError, CorruptedLogError

RECORD_PREFIX = b"WLE1"
CHECKPOINT_NAME = "checkpoint.json"
LOG_NAME = "watermark.log"


def crc32(data: bytes) -> int:
    return zlib.crc32(data) & 0xFFFFFFFF


def encode_record(record: dict) -> bytes:
    payload = base64.b64encode(
        json.dumps(record, ensure_ascii=False, separators=(",", ":"), sort_keys=True).encode("utf-8")
    )
    body = RECORD_PREFIX + b"\t" + payload
    checksum = f"{crc32(body):08x}".encode("ascii")
    return body + b"\t" + checksum + b"\n"


def decode_record(line: bytes) -> dict:
    parts = line.split(b"\t")
    if len(parts) != 3 or parts[0] != RECORD_PREFIX:
        raise ValueError("bad record framing")
    try:
        expected = int(parts[2], 16)
    except ValueError as exc:
        raise ValueError("bad checksum") from exc
    body = parts[0] + b"\t" + parts[1]
    if crc32(body) != expected:
        raise ValueError("checksum mismatch")
    return json.loads(base64.b64decode(parts[1]))


def fsync_directory(path: str) -> None:
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


class WalLog:
    def __init__(self, directory: str):
        self.path = os.path.join(directory, LOG_NAME)
        os.makedirs(directory, exist_ok=True)
        if not os.path.exists(self.path):
            with open(self.path, "wb"):
                pass
        self.position = self._recover_tail()

    def _recover_tail(self) -> int:
        with open(self.path, "rb") as stream:
            data = stream.read()
        offset = 0
        while offset < len(data):
            line_end = data.find(b"\n", offset)
            if line_end < 0:
                self._truncate_tail(offset)
                return offset
            line = data[offset:line_end]
            try:
                decode_record(line)
            except Exception as exc:
                has_later_line = line_end + 1 < len(data)
                if has_later_line:
                    try:
                        decode_record(data[line_end + 1 : data.find(b"\n", line_end + 1)])
                    except CorruptedLogError:
                        raise
                    except Exception:
                        pass
                    raise CorruptedLogError("checksum or framing error before end of log") from exc
                self._truncate_tail(offset)
                return offset
            offset = line_end + 1
        return offset

    def _truncate_tail(self, offset: int) -> None:
        with open(self.path, "r+b") as stream:
            stream.truncate(offset)
            stream.flush()
            os.fsync(stream.fileno())

    def append(self, record: dict) -> int:
        line = encode_record(record)
        with open(self.path, "ab") as stream:
            stream.write(line)
            stream.flush()
            os.fsync(stream.fileno())
        self.position += len(line)
        return self.position

    def records(self, start: int = 0) -> Iterator[tuple[int, dict]]:
        offset = start
        with open(self.path, "rb") as stream:
            stream.seek(start)
            while offset < self.position:
                line = stream.readline()
                if not line:
                    break
                next_offset = offset + len(line)
                yield offset, decode_record(line.rstrip(b"\n"))
                offset = next_offset


def save_checkpoint(directory: str, state: dict, log_position: int) -> None:
    target = os.path.join(directory, CHECKPOINT_NAME)
    wrapped = {"version": 1, "log_position": log_position, "state": state}
    checksum_input = json.dumps(
        [1, log_position, state], ensure_ascii=False, separators=(",", ":"), sort_keys=True
    ).encode("utf-8")
    wrapped["checksum"] = f"{crc32(checksum_input):08x}"
    payload = json.dumps(wrapped, ensure_ascii=False, separators=(",", ":"), sort_keys=True)
    fd, temporary = tempfile.mkstemp(prefix=".checkpoint-", dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, target)
        fsync_directory(directory)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def load_checkpoint(directory: str) -> dict | None:
    path = os.path.join(directory, CHECKPOINT_NAME)
    if not os.path.exists(path):
        return None
    with open(path, "rb") as stream:
        wrapped = json.loads(stream.read())
    state = wrapped["state"]
    log_position = wrapped["log_position"]
    checksum_input = json.dumps(
        [1, log_position, state], ensure_ascii=False, separators=(",", ":"), sort_keys=True
    ).encode("utf-8")
    expected = f"{crc32(checksum_input):08x}"
    if wrapped.get("checksum") != expected:
        raise CheckpointChecksumError("checkpoint checksum mismatch")
    return {"state": state, "log_position": log_position}
