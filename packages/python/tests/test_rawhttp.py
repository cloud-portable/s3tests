import unittest

from cloud_portable_s3tests._rawhttp import (
    Credentials, RawHttpError, RawRequest, parse_response, parse_xml_error, raw_request,
)
from helpers.rawserver import OK_RESPONSE, RawServer

CREDS = Credentials("AK", "SK")


class TestRawHttp(unittest.TestCase):
    def test_unsigned_requests_send_literal_bytes_with_no_auth_headers(self):
        srv = RawServer(OK_RESPONSE)
        try:
            res = raw_request(srv.url, RawRequest(
                method="PUT", path="/bucket/key", query={"b": ["2"], "a": ["1"]},
                headers=[("content-length", "-1"), ("x-custom", "yes"), ("authorization", "")],
                body=b"hi", sign=False))
            self.assertEqual(res.status, 200)
            self.assertEqual(res.headers["x-amz-request-id"], "R1")
            self.assertEqual(res.body, b"ok")
            head = srv.head()
            self.assertTrue(head.startswith("PUT /bucket/key?a=1&b=2 HTTP/1.1\r\n"))
            self.assertIn("content-length: -1\r\n", head)  # override wins, case preserved
            self.assertNotIn("Content-Length: 2", head)
            self.assertIn("x-custom: yes\r\n", head)
            self.assertIn("authorization: \r\n", head)
            self.assertNotIn("x-amz-date", head.lower())
            self.assertIn("\r\n\r\n", head)
        finally:
            srv.close()

    def test_signed_requests_cover_step_headers_but_never_content_length(self):
        srv = RawServer(OK_RESPONSE)
        try:
            raw_request(srv.url, RawRequest(
                method="PUT", path="/b/k", headers=[("x-amz-meta-foo", "bar"), ("Content-Length", "-1")],
                body=b"hi", sign=True, credentials=CREDS))
            head = srv.head()
            self.assertIn("Authorization: AWS4-HMAC-SHA256 Credential=AK/", head)
            self.assertIn("X-Amz-Date: ", head)
            self.assertIn("X-Amz-Content-Sha256: ", head)
            auth = [line for line in head.split("\r\n") if line.startswith("Authorization:")][0]
            signed = auth.split("SignedHeaders=")[1].split(",")[0]
            self.assertIn("x-amz-meta-foo", signed.split(";"))
            self.assertIn("host", signed.split(";"))
            self.assertNotIn("content-length", signed.split(";"))
            self.assertIn("Content-Length: -1\r\n", head)
        finally:
            srv.close()

    def test_authorization_override_wins_over_the_signed_value(self):
        srv = RawServer(OK_RESPONSE)
        try:
            raw_request(srv.url, RawRequest(
                method="GET", path="/b", headers=[("authorization", "")], sign=True, credentials=CREDS))
            head = srv.head()
            self.assertIn("authorization: \r\n", head)
            self.assertNotIn("AWS4-HMAC-SHA256", head)
            self.assertIn("X-Amz-Date: ", head)  # other signed headers still sent
        finally:
            srv.close()

    def test_response_parsing_content_length_chunked_read_to_close(self):
        r = parse_response(b"HTTP/1.1 404 Not Found\r\nContent-Type: application/xml\r\nContent-Length: 5\r\n\r\nhelloEXTRA")
        self.assertEqual((r.status, r.headers["content-type"], r.body), (404, "application/xml", b"hello"))
        r = parse_response(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n1;ext\r\n!\r\n0\r\n\r\n")
        self.assertEqual(r.body, b"hello!")
        r = parse_response(b"HTTP/1.1 200 OK\r\nConnection: close\r\nX-Dup: a\r\nX-Dup: b\r\n\r\nrest of stream")
        self.assertEqual(r.body, b"rest of stream")
        self.assertEqual(r.headers["x-dup"], "a")
        with self.assertRaises(RawHttpError):
            parse_response(b"garbage")

    def test_parse_xml_error(self):
        code, msg = parse_xml_error(b'<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>The &amp; key</Message></Error>')
        self.assertEqual((code, msg), ("NoSuchKey", "The & key"))
        self.assertEqual(parse_xml_error(b"<ListBucketResult/>"), ("", ""))
        self.assertEqual(parse_xml_error(b""), ("", ""))
        self.assertEqual(parse_xml_error(b"<Error><Code>X</Code></Error>"), ("X", ""))


if __name__ == "__main__":
    unittest.main()
