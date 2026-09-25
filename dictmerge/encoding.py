import struct

INT = "int"
STRING = "string"
FLOAT = "float"
BOOL = "bool"
BYTES = "bytes"

TYPES = frozenset({INT, STRING, FLOAT, BOOL, BYTES})

TAG_INT = 1
TAG_STRING = 2
TAG_FLOAT = 3
TAG_BOOL = 4
TAG_BYTES = 5

_TAGS = {
    INT: TAG_INT,
    STRING: TAG_STRING,
    FLOAT: TAG_FLOAT,
    BOOL: TAG_BOOL,
    BYTES: TAG_BYTES,
}
_TAG_TYPES = {tag: typ for typ, tag in _TAGS.items()}


def validate_type_name(value_type):
    if value_type not in TYPES:
        raise ValueError(f"unsupported column type {value_type!r}")


def check_value(value_type, value):
    validate_type_name(value_type)
    if value is None:
        raise ValueError("null is represented by the null bitmap, not a dictionary term")
    if value_type == INT:
        valid = isinstance(value, int) and not isinstance(value, bool)
    elif value_type == STRING:
        valid = isinstance(value, str)
    elif value_type == FLOAT:
        valid = isinstance(value, float)
    elif value_type == BOOL:
        valid = isinstance(value, bool)
    else:
        valid = isinstance(value, (bytes, bytearray))
    if not valid:
        raise TypeError(f"value {value!r} is not valid for type {value_type!r}")


def float_bits(value):
    return struct.unpack("<Q", struct.pack("<d", value))[0]


def bits_to_float(bits):
    return struct.unpack("<d", struct.pack("<Q", bits))[0]


def encode_value(value_type, value):
    check_value(value_type, value)
    if value_type == INT:
        return str(value).encode("ascii")
    if value_type == STRING:
        return value.encode("utf-8")
    if value_type == FLOAT:
        return struct.pack("<Q", float_bits(value))
    if value_type == BOOL:
        return b"\x01" if value else b"\x00"
    return bytes(value)


def decode_value(value_type, payload):
    validate_type_name(value_type)
    if value_type == INT:
        return int(payload.decode("ascii"))
    if value_type == STRING:
        return payload.decode("utf-8")
    if value_type == FLOAT:
        if len(payload) != 8:
            raise ValueError("float payload must be 8 bytes")
        return bits_to_float(struct.unpack("<Q", payload)[0])
    if value_type == BOOL:
        if payload not in (b"\x00", b"\x01"):
            raise ValueError("invalid boolean payload")
        return payload == b"\x01"
    return bytes(payload)


def value_tag(value_type):
    validate_type_name(value_type)
    return _TAGS[value_type]


def tag_type(tag):
    try:
        return _TAG_TYPES[tag]
    except KeyError as exc:
        raise ValueError(f"unknown value type tag {tag}") from exc


def values_equal(value_type, left, right):
    if left is None or right is None:
        return left is None and right is None
    if value_type == FLOAT:
        return float_bits(left) == float_bits(right)
    return left == right
