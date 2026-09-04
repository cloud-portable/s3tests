"""Per-vector dataset cache: multi-megabyte ``$prng`` streams are generated
once per vector, however many times their bytes or derived values
(``${data.<name>.<field>}``) are referenced. Derived fields are computed
locally from the cached bytes (the corpus datagen's ``derived`` regenerates
the dataset on every call); the semantics mirror the datagen reference and
are asserted equal in the tests."""

from __future__ import annotations

import base64
import hashlib
import struct
import zlib
from typing import Any, Mapping

from cloud_portable_s3vectors.datagen import generate


class DataError(ValueError):
    """An unknown dataset, field or invalid slice."""


class DataCache:
    def __init__(self, specs: Mapping[str, Any] | None) -> None:
        self.specs: Mapping[str, Any] = specs if specs is not None else {}
        self._bytes: dict[str, bytes] = {}
        self._derived: dict[tuple[str, str], str] = {}

    def bytes(self, name: str) -> bytes:
        """Dataset bytes, generated on first use (shared; never mutate)."""
        b = self._bytes.get(name)
        if b is None:
            spec = self.specs.get(name)
            if isinstance(spec, dict) and "$slice" in spec:
                # Resolve $slice through the cache: the corpus generate() would
                # regenerate the parent dataset on every slice call.
                d = spec["$slice"]
                parent = self.specs.get(d["of"])
                if parent is None:
                    raise DataError(f"slice '{name}' references unknown dataset '{d['of']}'")
                if isinstance(parent, dict) and "$slice" in parent:
                    raise DataError(f"slice '{name}' references slice '{d['of']}' (chained slices are not allowed)")
                base = self.bytes(d["of"])
                end = d["offset"] + d["length"]
                if end > len(base):
                    raise DataError(f"slice '{name}' [{d['offset']}, {end}) exceeds '{d['of']}' size {len(base)}")
                b = base[d["offset"] : end]
            else:
                try:
                    b = generate(self.specs, name)
                except (KeyError, ValueError) as err:
                    raise DataError(str(err).strip("'\"")) from err
            self._bytes[name] = b
        return b

    def derived(self, name: str, field: str) -> str:
        """A ``${data.<name>.<field>}`` value, memoized and computed from the
        cached bytes."""
        key = (name, field)
        v = self._derived.get(key)
        if v is None:
            v = derive_field(self.bytes(name), field)
            self._derived[key] = v
        return v


def derive_field(data: bytes, field: str) -> str:
    if field == "size":
        return str(len(data))
    if field == "md5":
        return hashlib.md5(data).hexdigest()
    if field == "etag":
        return f'"{hashlib.md5(data).hexdigest()}"'
    if field == "sha256":
        return hashlib.sha256(data).hexdigest()
    if field == "sha256B64":
        return base64.b64encode(hashlib.sha256(data).digest()).decode("ascii")
    if field == "sha1B64":
        return base64.b64encode(hashlib.sha1(data).digest()).decode("ascii")
    if field == "crc32B64":
        return base64.b64encode(struct.pack(">I", zlib.crc32(data) & 0xFFFFFFFF)).decode("ascii")
    if field == "crc32cB64":
        return base64.b64encode(struct.pack(">I", _crc32c(data))).decode("ascii")
    if field == "crc64nvmeB64":
        return base64.b64encode(struct.pack(">Q", _crc64nvme(data))).decode("ascii")
    raise DataError(f"unknown derived data field: {field}")


# The checksum implementations mirror the corpus datagen reference; the vdata
# tests assert equality with the corpus package's own derived() for every field.


def _make_table(poly: int, width: int) -> list[int]:
    mask = (1 << width) - 1
    table = []
    for n in range(256):
        c = n
        for _ in range(8):
            c = ((c >> 1) ^ poly) if c & 1 else (c >> 1)
        table.append(c & mask)
    return table


_CRC32C_TABLE = _make_table(0x82F63B78, 32)
_CRC64_TABLE = _make_table(0x9A6C9329AC4BC9B5, 64)


def _crc32c(data: bytes) -> int:
    c = 0xFFFFFFFF
    t = _CRC32C_TABLE
    for b in data:
        c = t[(c ^ b) & 0xFF] ^ (c >> 8)
    return (c ^ 0xFFFFFFFF) & 0xFFFFFFFF


def _crc64nvme(data: bytes) -> int:
    c = 0xFFFFFFFFFFFFFFFF
    t = _CRC64_TABLE
    for b in data:
        c = t[(c ^ b) & 0xFF] ^ (c >> 8)
    return c ^ 0xFFFFFFFFFFFFFFFF
