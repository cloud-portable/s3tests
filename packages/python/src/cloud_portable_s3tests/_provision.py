"""Prerequisite provisioning and best-effort teardown against the endpoint
under test itself (the default provisioner), using the main identity."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional, Protocol

from botocore.exceptions import ClientError

from ._rawhttp import Cancellable


@dataclass
class Target:
    """The endpoint under test, as passed to Provisioner methods."""

    endpoint: str
    region: str
    client: Any  # the runner's main-identity boto3 S3 client


@dataclass
class BucketInfo:
    name: str
    # Keys known to have been written, deleted explicitly during teardown
    # (listings on some implementations hide "foo/bar" while object "foo"
    # exists).
    known_keys: list[str] = field(default_factory=list)


@dataclass
class ObjectInfo:
    key: str
    etag: str
    version_id: str


class Provisioner(Protocol):
    def bucket(self, target: Target, prereq: dict, name: str, cancel: Optional[Cancellable]) -> BucketInfo: ...

    def object(
        self, target: Target, prereq: dict, bucket_name: str, body: Optional[bytes], cancel: Optional[Cancellable]
    ) -> ObjectInfo: ...

    def teardown(self, target: Target, buckets: list[BucketInfo], cancel: Optional[Cancellable]) -> list[str]:
        """Best-effort cleanup; returns human-readable warnings, never raises."""
        ...


def _msg(err: BaseException) -> str:
    if isinstance(err, ClientError):
        e = err.response.get("Error", {}) or {}
        code, msg = e.get("Code", ""), e.get("Message", "")
        return f"{code}: {msg}" if code else str(err)
    return str(err)


def _code(err: BaseException) -> str:
    if isinstance(err, ClientError):
        return str((err.response.get("Error", {}) or {}).get("Code", ""))
    return ""


class DefaultProvisioner:
    """The built-in Provisioner: CreateBucket/PutObject via the runner's client."""

    def bucket(self, target: Target, prereq: dict, name: str, cancel: Optional[Cancellable] = None) -> BucketInfo:
        kwargs: dict[str, Any] = {"Bucket": name}
        if prereq.get("objectLock") is True:
            kwargs["ObjectLockEnabledForBucket"] = True
        if target.region != "us-east-1":
            kwargs["CreateBucketConfiguration"] = {"LocationConstraint": target.region}
        try:
            target.client.create_bucket(**kwargs)
        except Exception as err:  # noqa: BLE001
            raise RuntimeError(f"CreateBucket {name}: {_msg(err)}") from err
        versioning = prereq.get("versioning")
        if versioning:
            try:
                target.client.put_bucket_versioning(Bucket=name, VersioningConfiguration={"Status": versioning})
            except Exception as err:  # noqa: BLE001
                raise RuntimeError(f"PutBucketVersioning {name}={versioning}: {_msg(err)}") from err
        return BucketInfo(name=name)

    def object(
        self, target: Target, prereq: dict, bucket_name: str, body: Optional[bytes], cancel: Optional[Cancellable] = None
    ) -> ObjectInfo:
        kwargs: dict[str, Any] = {"Bucket": bucket_name, "Key": prereq["key"], "Body": body if body is not None else b""}
        if prereq.get("contentType"):
            kwargs["ContentType"] = prereq["contentType"]
        if prereq.get("metadata"):
            kwargs["Metadata"] = prereq["metadata"]
        try:
            out = target.client.put_object(**kwargs)
        except Exception as err:  # noqa: BLE001
            raise RuntimeError(f"PutObject {bucket_name}/{prereq['key']}: {_msg(err)}") from err
        return ObjectInfo(key=prereq["key"], etag=out.get("ETag", "") or "", version_id=out.get("VersionId", "") or "")

    def teardown(self, target: Target, buckets: list[BucketInfo], cancel: Optional[Cancellable] = None) -> list[str]:
        warnings: list[str] = []
        for b in buckets:
            warnings.extend(teardown_bucket(target.client, b.name, b.known_keys))
        return warnings


default_provisioner = DefaultProvisioner()


def _is_no_such_bucket(err: BaseException) -> bool:
    return _code(err) in ("NoSuchBucket", "NotFound", "404")


def teardown_bucket(client, bucket: str, known_keys: list[str]) -> list[str]:
    """Empty and delete one bucket, best-effort: abort multipart uploads,
    delete known-written keys explicitly (some servers' listings hide keys),
    delete every version and delete marker (bypassing governance retention
    and lifting legal holds on object-lock buckets), then delete the bucket."""
    warnings: list[str] = []

    # Abort in-flight multipart uploads.
    try:
        key_marker = upload_id_marker = None
        while True:
            kwargs: dict[str, Any] = {"Bucket": bucket}
            if key_marker is not None:
                kwargs["KeyMarker"] = key_marker
            if upload_id_marker is not None:
                kwargs["UploadIdMarker"] = upload_id_marker
            mu = client.list_multipart_uploads(**kwargs)
            for u in mu.get("Uploads") or []:
                try:
                    client.abort_multipart_upload(Bucket=bucket, Key=u["Key"], UploadId=u["UploadId"])
                except Exception as err:  # noqa: BLE001
                    warnings.append(f"teardown {bucket}: AbortMultipartUpload {u['Key']}: {_msg(err)}")
            if not mu.get("IsTruncated"):
                break
            key_marker, upload_id_marker = mu.get("NextKeyMarker"), mu.get("NextUploadIdMarker")
            if key_marker is None:
                break
    except Exception as err:  # noqa: BLE001
        if _is_no_such_bucket(err):
            return warnings  # already gone — nothing to do
        warnings.append(f"teardown {bucket}: ListMultipartUploads: {_msg(err)}")

    # Delete keys the runner knows it wrote, in case the server's listings
    # miss them (best-effort; the sweep below reports anything that fails).
    for key in known_keys:
        try:
            client.delete_object(Bucket=bucket, Key=key)
        except Exception:  # noqa: BLE001 - the sweep reports residuals
            pass

    # AWS rejects BypassGovernanceRetention on buckets without object lock,
    # so only send it (and lift legal holds) where lock is configured.
    locked = _bucket_has_object_lock(client, bucket)

    try:
        key_marker = version_id_marker = None
        while True:
            kwargs = {"Bucket": bucket}
            if key_marker is not None:
                kwargs["KeyMarker"] = key_marker
            if version_id_marker is not None:
                kwargs["VersionIdMarker"] = version_id_marker
            lv = client.list_object_versions(**kwargs)
            ids: list[dict[str, str]] = []
            for v in lv.get("Versions") or []:
                if locked:
                    try:
                        client.put_object_legal_hold(
                            Bucket=bucket, Key=v["Key"], VersionId=v.get("VersionId"), LegalHold={"Status": "OFF"}
                        )
                    except Exception:  # noqa: BLE001 - legal holds block deletion even with bypass
                        pass
                ids.append(_version_id(v))
            for m in lv.get("DeleteMarkers") or []:
                ids.append(_version_id(m))
            for i in range(0, len(ids), 1000):
                kwargs2: dict[str, Any] = {"Bucket": bucket, "Delete": {"Objects": ids[i : i + 1000], "Quiet": True}}
                if locked:
                    kwargs2["BypassGovernanceRetention"] = True
                try:
                    out = client.delete_objects(**kwargs2)
                    for e in out.get("Errors") or []:
                        warnings.append(
                            f"teardown {bucket}: delete {e.get('Key')} ({e.get('VersionId', '') or ''}): {e.get('Message')}"
                        )
                except Exception as err:  # noqa: BLE001
                    warnings.append(f"teardown {bucket}: DeleteObjects: {_msg(err)}")
            if not lv.get("IsTruncated"):
                break
            key_marker, version_id_marker = lv.get("NextKeyMarker"), lv.get("NextVersionIdMarker")
            if key_marker is None:
                break
    except Exception as err:  # noqa: BLE001
        warnings.append(f"teardown {bucket}: ListObjectVersions: {_msg(err)}")

    try:
        client.delete_bucket(Bucket=bucket)
    except Exception as err:  # noqa: BLE001
        if not _is_no_such_bucket(err):
            warnings.append(f"teardown {bucket}: DeleteBucket: {_msg(err)}")
    return warnings


def _version_id(v: dict) -> dict[str, str]:
    out = {"Key": v["Key"]}
    vid = v.get("VersionId")
    if vid is not None and vid != "":
        out["VersionId"] = vid
    return out


def _bucket_has_object_lock(client, bucket: str) -> bool:
    try:
        out = client.get_object_lock_configuration(Bucket=bucket)
        return (out.get("ObjectLockConfiguration") or {}).get("ObjectLockEnabled") == "Enabled"
    except Exception:  # noqa: BLE001 - typically ObjectLockConfigurationNotFoundError
        return False
