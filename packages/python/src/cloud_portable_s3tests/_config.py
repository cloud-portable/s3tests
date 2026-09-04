"""Config defaults and the per-identity boto3 client registry."""

from __future__ import annotations

import base64
import binascii
import hashlib
import threading
from dataclasses import dataclass, field, replace
from typing import Any, Callable, Optional, Union

import boto3
from botocore import UNSIGNED, handlers
from botocore.config import Config as BotoConfig

IDENTITY_MAIN = "main"
IDENTITY_ANONYMOUS = "anonymous"
IDENTITY_INVALID = "invalid"


@dataclass
class Credential:
    access_key_id: str
    secret_access_key: str
    session_token: str = ""
    # Attributes of a $credential identity exposed as ${res.<handle>.*}.
    canonical_id: str = ""
    display_name: str = ""


# Well-formed credentials under an access key the server cannot know (same
# constants as the Go and JS runners).
INVALID_CREDENTIALS = Credential("AKIAS3TESTSINVALID00", "s3tests-invalid-secret-key-0000000000000")

ProvisionCredential = Callable[[str], Credential]


@dataclass
class Config:
    """Runner configuration; ``endpoint`` and ``credentials`` are required."""

    endpoint: str
    credentials: Optional[Credential]
    region: str = "us-east-1"
    virtual_host_style: bool = False
    concurrency: int = 1
    bucket_prefix: str = "s3tests-"
    # Supplies credentials for $credential prerequisites (a second identity),
    # called with the handle; vectors needing one are blocked when unset.
    provision_credential: Optional[ProvisionCredential] = None
    # Overrides the default bucket/object provisioning and teardown.
    provisioner: Any = None
    keep_resources: bool = False
    request_timeout_ms: int = 60_000
    _extra: dict = field(default_factory=dict, repr=False)


def with_defaults(config: Config) -> Config:
    if not isinstance(config.endpoint, str) or config.endpoint == "":
        raise ValueError("s3tests: config.endpoint is required")
    if config.credentials is None or not isinstance(config.credentials.access_key_id, str):
        raise ValueError("s3tests: config.credentials is required")
    return replace(config, concurrency=max(1, int(config.concurrency or 1)))


def build_client(cfg: Config, credentials: Union[Credential, str]):
    """An S3 client tuned for compatibility testing: the vectors own their wire
    bytes (no implicit checksums, no retries, no Expect: 100-continue) and
    expectations need the first response, verbatim."""
    anonymous = credentials == IDENTITY_ANONYMOUS
    timeout = max(1, cfg.request_timeout_ms // 1000)
    boto_cfg = BotoConfig(
        signature_version=UNSIGNED if anonymous else "s3v4",
        retries={"total_max_attempts": 1},
        request_checksum_calculation="when_required",
        response_checksum_validation="when_required",
        s3={"addressing_style": "virtual" if cfg.virtual_host_style else "path"},
        parameter_validation=False,
        connect_timeout=timeout,
        read_timeout=timeout,
        max_pool_connections=max(10, cfg.concurrency),
    )
    kwargs: dict[str, Any] = {}
    if not anonymous:
        assert isinstance(credentials, Credential)
        kwargs = {
            "aws_access_key_id": credentials.access_key_id,
            "aws_secret_access_key": credentials.secret_access_key,
            "aws_session_token": credentials.session_token or None,
        }
    client = boto3.client("s3", endpoint_url=cfg.endpoint, region_name=cfg.region, config=boto_cfg, **kwargs)
    _tune_events(client)
    return client


# botocore rewrites S3 wire bytes through handlers the runner must switch
# off: the vectors decide what goes on the wire.
_SSE_OPS = (
    "HeadObject", "GetObject", "PutObject", "CopyObject", "CreateMultipartUpload",
    "UploadPart", "UploadPartCopy", "CompleteMultipartUpload", "SelectObjectContent",
)
_LIST_OPS = ("ListObjects", "ListObjectsV2", "ListObjectVersions")


def _tune_events(client) -> None:
    ev = client.meta.events
    ev.unregister("before-call.s3", handlers.add_expect_header)  # Expect: 100-continue
    ev.unregister("before-parameter-build.s3", handlers.validate_bucket_name)  # the server decides
    for op in _LIST_OPS:  # EncodingType=url injection + key decoding
        ev.unregister(f"before-parameter-build.s3.{op}", handlers.set_list_objects_encoding_type_url)
    ev.unregister("after-call.s3.ListObjects", handlers.decode_list_object)
    ev.unregister("after-call.s3.ListObjectsV2", handlers.decode_list_object_v2)
    ev.unregister("after-call.s3.ListObjectVersions", handlers.decode_list_object_versions)
    for op in ("CopyObject", "UploadPartCopy"):
        ev.unregister(f"before-parameter-build.s3.{op}", handlers.handle_copy_source_param)  # URL-quotes
        ev.unregister(f"before-parameter-build.s3.{op}", handlers.copy_source_sse_md5)
    for op in _SSE_OPS:
        ev.unregister(f"before-parameter-build.s3.{op}", handlers.sse_md5)
    ev.register("before-parameter-build.s3", _ssec_params)
    ev.register("before-call.s3", _before_call)


def _before_call(params, **kwargs) -> None:
    # No Expect: 100-continue, and never a second request via the region
    # redirector (retries are off; the vectors want the first response).
    params.get("headers", {}).pop("Expect", None)
    ctx = params.get("context", {})
    if isinstance(ctx.get("s3_redirect"), dict):
        ctx["s3_redirect"]["redirected"] = True


def _ssec_params(params, **kwargs) -> None:
    """SSE-C keys as the JS runner's SDK sends them: a key that is already
    valid base64 of 32 bytes goes out verbatim, anything else is
    base64-encoded, and the MD5 is always recomputed from the key bytes."""
    for target, hash_member in (
        ("SSECustomerKey", "SSECustomerKeyMD5"),
        ("CopySourceSSECustomerKey", "CopySourceSSECustomerKeyMD5"),
    ):
        value = params.get(target)
        if not value:
            continue
        if isinstance(value, str):
            raw = _decode_ssec_key(value)
            if raw is None:
                raw = value.encode("utf-8")
                params[target] = base64.b64encode(raw).decode("ascii")
        else:
            raw = bytes(value)
            params[target] = base64.b64encode(raw).decode("ascii")
        params[hash_member] = base64.b64encode(hashlib.md5(raw).digest()).decode("ascii")


def _decode_ssec_key(s: str) -> bytes | None:
    try:
        raw = base64.b64decode(s, validate=True)
    except (binascii.Error, ValueError):
        return None
    return raw if len(raw) == 32 else None


class Identities:
    """Per-run identity registry: lazily built clients + raw credentials."""

    def __init__(self, cfg: Config) -> None:
        self.cfg = cfg
        self._lock = threading.Lock()
        self.clients: dict[str, Any] = {}
        self._alt: dict[str, Union[Credential, BaseException]] = {}

    def resolve_credentials(self, identity: str) -> Optional[Credential]:
        """Raw credentials for an identity (used by $http signing); None for
        anonymous."""
        if identity == IDENTITY_MAIN:
            return self.cfg.credentials
        if identity == IDENTITY_INVALID:
            return INVALID_CREDENTIALS
        if identity == IDENTITY_ANONYMOUS:
            return None
        return self.provision_alt(identity)

    def provision_alt(self, handle: str) -> Credential:
        """Provisioned $credential identity, once per run per handle (failures
        are cached too: one provisioning attempt per run)."""
        if self.cfg.provision_credential is None:
            raise RuntimeError(
                f"no provision_credential configured (required for $credential prerequisite {handle!r})"
            )
        with self._lock:
            got = self._alt.get(handle)
            if got is None:
                try:
                    got = self.cfg.provision_credential(handle)
                except BaseException as err:  # noqa: BLE001 - cached and re-raised
                    got = err
                self._alt[handle] = got
        if isinstance(got, BaseException):
            raise got
        return got

    def client(self, identity: str):
        """The (cached) S3 client for an identity."""
        with self._lock:
            c = self.clients.get(identity)
        if c is not None:
            return c
        creds: Union[Credential, str]
        if identity == IDENTITY_ANONYMOUS:
            creds = IDENTITY_ANONYMOUS
        else:
            resolved = self.resolve_credentials(identity)
            assert resolved is not None
            creds = resolved
        c = build_client(self.cfg, creds)
        with self._lock:
            return self.clients.setdefault(identity, c)
