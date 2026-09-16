"""Per-vector dataset cache: multi-megabyte ``$prng`` streams are generated
once per vector, however many times their bytes or derived values
(``${data.<name>.<field>}``) are referenced. Derived fields are computed
locally from those cached bytes — the corpus ``derived`` is bounded in memory
but re-reads the dataset per field, so one materialization plus N local hashes
is cheaper here. The semantics mirror the datagen reference and are asserted
equal against it in the tests."""

from __future__ import annotations

import base64
import io
import hashlib
import struct
import zlib
from typing import Any, Mapping

from cloud_portable_s3vectors.datagen import data_size, generate, generate_range

#: Dataset size above which a caller should stream rather than materialize.
#: Below it, caching wins: bodies and digests reference the same dataset
#: repeatedly. Above it, holding the bytes is what makes a gigabyte-scale vector
#: unrunnable. Keep in step with the Go runner's ``vdata.StreamThreshold``.
STREAM_THRESHOLD = 8 * 1024 * 1024


class DatasetReader(io.RawIOBase):
    """A seekable read-only file object over a dataset, for boto3's ``Body``.

    The corpus exposes an iterator of chunks; botocore wants ``read(n)`` and
    seeks the body to size it and to rewind after signing, so reads are served
    as ranges instead. Nothing is cached: the point is to never hold the bytes.
    """

    def __init__(self, specs, name: str) -> None:
        self._specs = specs
        self._name = name
        self._size = data_size(specs, name)
        self._pos = 0

    def readable(self) -> bool:
        return True

    def seekable(self) -> bool:
        return True

    def __len__(self) -> int:
        return self._size

    def read(self, size: int = -1) -> bytes:
        if size is None or size < 0:
            size = self._size - self._pos
        size = min(size, self._size - self._pos)
        if size <= 0:
            return b""
        chunk = generate_range(self._specs, self._name, self._pos, size)
        self._pos += size
        return chunk

    def readinto(self, buf) -> int:
        chunk = self.read(len(buf))
        buf[: len(chunk)] = chunk
        return len(chunk)

    def seek(self, offset: int, whence: int = io.SEEK_SET) -> int:
        if whence == io.SEEK_SET:
            pos = offset
        elif whence == io.SEEK_CUR:
            pos = self._pos + offset
        elif whence == io.SEEK_END:
            pos = self._size + offset
        else:
            raise ValueError(f"invalid whence {whence}")
        if pos < 0:
            raise ValueError(f"negative position {pos}")
        self._pos = pos
        return pos

    def tell(self) -> int:
        return self._pos


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
            try:
                b = generate(self.specs, name)
            except (KeyError, ValueError) as err:
                raise DataError(str(err).strip("'\"")) from err
            self._bytes[name] = b
        return b

    def size(self, name: str) -> int:
        """The dataset's declared length in bytes, generating nothing."""
        return data_size(self.specs, name)

    def reader(self, name: str) -> DatasetReader:
        """A seekable file object over the dataset; never cached."""
        return DatasetReader(self.specs, name)

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
