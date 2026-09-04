import unittest
from datetime import datetime, timezone

from botocore.session import get_session

from cloud_portable_s3tests._coerce import CoerceError, build_input, parse_time

MODEL = get_session().get_service_model("s3")


def coerce(op, params, resolve=None):
    return build_input(MODEL.operation_model(op), params, resolve)


class TestCoerce(unittest.TestCase):
    def test_timestamps_convert_to_datetimes_in_all_corpus_formats(self):
        inp, _ = coerce("CopyObject", {"CopySourceIfModifiedSince": "2026-01-02T03:04:05Z"})
        self.assertEqual(inp["CopySourceIfModifiedSince"], datetime(2026, 1, 2, 3, 4, 5, tzinfo=timezone.utc))
        inp, _ = coerce("PutObject", {"Expires": "Sun, 01 Jan 2034 00:00:00 GMT"})
        self.assertEqual(inp["Expires"].year, 2034)
        inp, _ = coerce("PutBucketLifecycleConfiguration", {"LifecycleConfiguration": {"Rules": [{
            "Expiration": {"Date": "2023-09-27"}, "Transitions": [{"Date": "20220927", "StorageClass": "GLACIER"}]}]}})
        rule = inp["LifecycleConfiguration"]["Rules"][0]
        self.assertEqual(rule["Expiration"]["Date"], datetime(2023, 9, 27, tzinfo=timezone.utc))
        self.assertEqual(rule["Transitions"][0]["Date"], datetime(2022, 9, 27, tzinfo=timezone.utc))
        inp, _ = coerce("DeleteObjects", {"Delete": {"Objects": [{"Key": "k", "LastModifiedTime": "2026-01-01T00:00:00Z"}]}})
        self.assertIsInstance(inp["Delete"]["Objects"][0]["LastModifiedTime"], datetime)
        with self.assertRaises(CoerceError):
            parse_time("not a time")
        with self.assertRaises(CoerceError):
            coerce("PutObject", {"Expires": "garbage"})

    def test_body_content_descriptors_resolve_to_bytes_and_are_held_aside(self):
        inp, body = coerce("PutObject", {"Bucket": "b", "Body": {"$data": "part1"}}, lambda n: b"DATA")
        self.assertEqual(body, b"DATA")
        self.assertEqual(inp["Body"], body)
        _, plain = coerce("UploadPart", {"Body": "hello"})
        self.assertEqual(plain, b"hello")

    def test_copy_source_object_form_composes_the_string(self):
        self.assertEqual(coerce("CopyObject", {"CopySource": {"Bucket": "b", "Key": "src"}})[0]["CopySource"], "b/src")
        self.assertEqual(coerce("CopyObject", {"CopySource": {"Bucket": "b", "Key": "src", "VersionId": "v1"}})[0]["CopySource"], "b/src?versionId=v1")
        self.assertEqual(coerce("CopyObject", {"CopySource": "b/plain"})[0]["CopySource"], "b/plain")

    def test_sse_c_keys_stay_base64_strings(self):
        key = "pO3upElrwuEXSoFwCfnZPdSsmt/xWeFa0N9KgDijwVs="
        self.assertEqual(coerce("PutObject", {"SSECustomerKey": {"$base64": key}})[0]["SSECustomerKey"], key)
        self.assertEqual(coerce("PutObject", {"SSECustomerKey": key})[0]["SSECustomerKey"], key)

    def test_numbers_for_string_members_become_strings(self):
        # botocore's model decides: string-typed members written as bare
        # numbers are stringified, integer-typed members stay numbers.
        self.assertEqual(coerce("ListParts", {"Bucket": 123})[0]["Bucket"], "123")
        self.assertEqual(coerce("ListParts", {"PartNumberMarker": 3})[0]["PartNumberMarker"], 3)
        self.assertEqual(coerce("ListParts", {"MaxParts": 1})[0]["MaxParts"], 1)

    def test_unions_pass_through_as_plain_dicts(self):
        inp, _ = coerce("PutBucketMetricsConfiguration", {"MetricsConfiguration": {"Id": "x", "Filter": {"Prefix": "documents/"}}})
        self.assertEqual(inp["MetricsConfiguration"]["Filter"], {"Prefix": "documents/"})
        # Unknown members pass through untouched (the server decides).
        self.assertEqual(coerce("PutObject", {"Bogus": 1})[0]["Bogus"], 1)


if __name__ == "__main__":
    unittest.main()
