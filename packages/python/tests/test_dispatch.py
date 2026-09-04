import unittest
from datetime import datetime, timezone

from cloud_portable_s3tests._config import IDENTITY_ANONYMOUS, Config, Credential, Identities, build_client, with_defaults
from cloud_portable_s3tests._dispatch import call, go_rfc3339_nano, supported
from helpers.canned import CannedServer

XML = {"content-type": "application/xml"}


def client(url):
    cfg = with_defaults(Config(endpoint=url, credentials=Credential("AK", "SK")))
    return build_client(cfg, cfg.credentials)


class TestDispatch(unittest.TestCase):
    def test_supported(self):
        c = client("http://127.0.0.1:1")
        for op in ["GetObject", "PutObject", "CreateMultipartUpload", "ListObjectsV2", "DeleteBucket"]:
            self.assertTrue(supported(c, op), op)
        # botocore still models the legacy operation the Go/JS SDKs dropped.
        self.assertTrue(supported(c, "PutBucketLifecycle"))
        self.assertFalse(supported(c, "NoSuchOperation"))

    def test_success_path_captures_raw_status_headers_and_walks_output(self):
        srv = CannedServer(lambda r: (200, {**XML, "x-amz-request-id": "REQ1"},
            b'<?xml version="1.0"?><InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>UP123</UploadId></InitiateMultipartUploadResult>'))
        try:
            res = call(client(srv.url), "CreateMultipartUpload", {"Bucket": "b", "Key": "k"}, None)
            self.assertIsNone(res.err)
            self.assertEqual(res.status, 200)
            self.assertEqual(res.headers["x-amz-request-id"], "REQ1")
            self.assertEqual(res.output["UploadId"], "UP123")
            self.assertNotIn("ResponseMetadata", res.output)
        finally:
            srv.close()

    def test_error_path_maps_code_message_and_keeps_raw_status(self):
        srv = CannedServer(lambda r: (404, XML,
            b'<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>'))
        try:
            res = call(client(srv.url), "GetObject", {"Bucket": "b", "Key": "missing"}, None)
            self.assertIsNotNone(res.err)
            self.assertEqual(res.status, 404)
            self.assertEqual(res.code, "NoSuchKey")
            self.assertEqual(res.msg, "The specified key does not exist.")
        finally:
            srv.close()

    def test_bodyless_errors_normalize_to_the_shared_code_names(self):
        srv = CannedServer(lambda r: (404 if r["method"] == "HEAD" else 304, {}, b""))
        try:
            c = client(srv.url)
            self.assertEqual(call(c, "HeadObject", {"Bucket": "b", "Key": "k"}, None).code, "NotFound")
            self.assertEqual(call(c, "GetObject", {"Bucket": "b", "Key": "k", "IfNoneMatch": '"e"'}, None).code, "NotModified")
        finally:
            srv.close()

    def test_get_object_drains_the_streaming_body_and_renders_go_style_dates(self):
        srv = CannedServer(lambda r: (200, {"content-type": "text/plain", "last-modified": "Mon, 02 Jan 2006 15:04:05 GMT"}, b"hello body"))
        try:
            res = call(client(srv.url), "GetObject", {"Bucket": "b", "Key": "k"}, None)
            self.assertIsNone(res.err)
            self.assertEqual(res.body, b"hello body")
            self.assertNotIn("Body", res.output, "stream excluded from the generic value")
            self.assertEqual(res.output["ContentLength"], 10)
            self.assertEqual(res.output["LastModified"], "2006-01-02T15:04:05Z")
        finally:
            srv.close()

    def test_wire_discipline_single_attempt_no_expect_header_no_implicit_checksums_verbatim_sse_c(self):
        srv = CannedServer(lambda r: (500, XML, b'<?xml version="1.0"?><Error><Code>Boom</Code><Message>x</Message></Error>'))
        try:
            key = "pO3upElrwuEXSoFwCfnZPdSsmt/xWeFa0N9KgDijwVs="
            call(client(srv.url), "PutObject", {"Bucket": "b", "Key": "k", "Body": "data", "SSECustomerKey": key,
                                                "SSECustomerAlgorithm": "AES256", "SSECustomerKeyMD5": "md5md5md5md5md5md5md5w=="}, None)
            self.assertEqual(len(srv.requests), 1, "retries must be off")
            h = srv.requests[0]["headers"]
            self.assertNotIn("expect", h, "Expect: 100-continue must be disabled")
            self.assertNotIn("x-amz-checksum-crc32", h)
            self.assertNotIn("x-amz-sdk-checksum-algorithm", h)
            self.assertEqual(h["x-amz-server-side-encryption-customer-key"], key, "SSE-C key sent verbatim")
            self.assertEqual(srv.requests[0]["body"], b"data")
        finally:
            srv.close()

    def test_copy_source_and_listings_go_out_verbatim(self):
        srv = CannedServer(lambda r: (500, XML, b'<?xml version="1.0"?><Error><Code>Boom</Code><Message>x</Message></Error>'))
        try:
            c = client(srv.url)
            call(c, "CopyObject", {"Bucket": "b", "Key": "k", "CopySource": "b/my-obj%3Ftest%26data"}, None)
            self.assertEqual(srv.requests[-1]["headers"]["x-amz-copy-source"], "b/my-obj%3Ftest%26data")
            call(c, "CopyObject", {"Bucket": "b", "Key": "k", "CopySource": {"Bucket": "b", "Key": "a b", "VersionId": "v1"}}, None)
            self.assertEqual(srv.requests[-1]["headers"]["x-amz-copy-source"], "b/a b?versionId=v1")
            call(c, "ListObjects", {"Bucket": "b"}, None)
            self.assertNotIn("encoding-type", srv.requests[-1]["url"])
        finally:
            srv.close()

    def test_anonymous_identity_sends_no_auth_headers(self):
        srv = CannedServer(lambda r: (200, XML, b'<?xml version="1.0"?><ListAllMyBucketsResult></ListAllMyBucketsResult>'))
        try:
            ids = Identities(with_defaults(Config(endpoint=srv.url, credentials=Credential("AK", "SK"))))
            call(ids.client(IDENTITY_ANONYMOUS), "ListBuckets", {}, None)
            h = srv.requests[0]["headers"]
            self.assertNotIn("authorization", h, f"anonymous request must be unsigned: {sorted(h)}")
            self.assertNotIn("x-amz-date", h)
        finally:
            srv.close()

    def test_go_rfc3339_nano_matches_go_formatting(self):
        self.assertEqual(go_rfc3339_nano(datetime(2026, 1, 2, 3, 4, 5, tzinfo=timezone.utc)), "2026-01-02T03:04:05Z")
        self.assertEqual(go_rfc3339_nano(datetime(2026, 1, 2, 3, 4, 5, 120000, tzinfo=timezone.utc)), "2026-01-02T03:04:05.12Z")
        self.assertEqual(go_rfc3339_nano(datetime(2026, 1, 2, 3, 4, 5, 7000, tzinfo=timezone.utc)), "2026-01-02T03:04:05.007Z")


if __name__ == "__main__":
    unittest.main()
