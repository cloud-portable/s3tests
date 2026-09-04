import unittest

from cloud_portable_s3tests._match import (
    MatchError, compile_regex, content_value, match_body, match_error, match_headers, match_value,
)


class TestMatch(unittest.TestCase):
    def test_scalar_equality(self):
        for expected, actual, ok in [
            ("a", "a", True),
            ("a", "b", False),
            (5, 5, True),
            (5, "5", False),  # string is not a number
            (True, True, True),
            (True, 1, False),  # bool is not a number
            (None, None, True),
            (10485760, 10485760, True),
        ]:
            ms = match_value("x", expected, actual, True)
            self.assertEqual(len(ms) == 0, ok, repr((expected, actual)))

    def test_subset_objects(self):
        actual = {"Key": "a", "Size": 5, "Extra": "ignored"}
        self.assertEqual(match_value("", {"Key": "a", "Size": 5}, actual, True), [])
        ms = match_value("", {"Key": "b"}, actual, True)
        self.assertEqual(len(ms), 1)
        self.assertEqual(ms[0].path, "Key")
        self.assertEqual(len(match_value("", {"Nope": "x"}, actual, True)), 1)

    def test_arrays_are_exact_length_and_ordered(self):
        actual = [{"Key": "a"}, {"Key": "b"}]
        self.assertEqual(match_value("", [{"Key": "a"}, {"Key": "b"}], actual, True), [])
        self.assertNotEqual(match_value("", [{"Key": "b"}, {"Key": "a"}], actual, True), [])
        self.assertNotEqual(match_value("", [{"Key": "a"}], actual, True), [])

    def test_assertion_operators(self):
        for name, expected, actual, present, ok in [
            ("exists yes", {"$exists": True}, "v", True, True),
            ("exists no", {"$exists": True}, None, False, False),
            ("absent yes", {"$absent": True}, None, False, True),
            ("absent no", {"$absent": True}, "v", True, False),
            ("eq assertion-looking literal", {"$eq": {"$exists": True}}, {"$exists": True}, True, True),
            ("eq scalar", {"$eq": 5}, 5, True, True),
            ("matches", {"$matches": '-2"$'}, '"abc-2"', True, True),
            ("matches no", {"$matches": '-2"$'}, '"abc-3"', True, False),
            ("matches number actual", {"$matches": "^20"}, 2026, True, True),
            ("length arr", {"$length": 2}, ["a", "b"], True, True),
            ("length str", {"$length": 3}, "abc", True, True),
            ("length wrong", {"$length": 3}, ["a"], True, False),
            ("contains", {"$contains": {"Key": "b"}}, [{"Key": "a"}, {"Key": "b", "X": 1}], True, True),
            ("contains no", {"$contains": "z"}, ["a", "b"], True, False),
            ("combined", {"$exists": True, "$matches": "^a"}, "abc", True, True),
            ("ne differs", {"$ne": '"abc"'}, '"def"', True, True),
            ("ne equal", {"$ne": '"abc"'}, '"abc"', True, False),
            ("ne absent", {"$ne": '"abc"'}, None, False, False),
            ("ne combined", {"$ne": '"s"', "$matches": "-"}, '"comp-2"', True, True),
        ]:
            ms = match_value("x", expected, actual, present)
            self.assertEqual(len(ms) == 0, ok, name + ": " + repr(ms))

    def test_portable_regexes_compile_natively_lookaheads_are_absent_from_the_corpus(self):
        for pat in ['-2"$', r"^\d{4}-\d{2}-\d{2}", "^(STANDARD|REDUCED_REDUNDANCY|)$"]:
            compile_regex(pat)

    def test_headers(self):
        headers = {"content-range": "bytes 0-4/10", "x-amz-request-id": "R1"}
        ms = match_headers(
            {"content-range": "bytes 0-4/10", "x-amz-request-id": {"$exists": True}, "x-amz-missing": {"$absent": True}},
            headers,
        )
        self.assertEqual(ms, [])
        self.assertEqual(len(match_headers({"content-range": "bytes 0-5/10"}, headers)), 1)

    def test_error_matching(self):
        self.assertEqual(match_error("NoSuchKey", "NoSuchKey", "nope"), [])
        self.assertEqual(len(match_error("NoSuchKey", "NoSuchBucket", "")), 1)
        obj = {"code": "InvalidURI", "message": "Couldn't parse the specified URI."}
        self.assertEqual(match_error(obj, "InvalidURI", "Couldn't parse the specified URI."), [])
        self.assertEqual(len(match_error(obj, "InvalidURI", "other")), 1)
        with self.assertRaises(MatchError):
            match_error(42, "x", "y")

    def test_body_matching(self):
        body = b"hello"
        self.assertEqual(match_body("hello", body, None), [])
        self.assertEqual(len(match_body("world", body, None)), 1)
        self.assertEqual(match_body({"$base64": "aGVsbG8="}, body, None), [])
        self.assertEqual(match_body({"$data": "part1"}, body, lambda n: b"hello"), [])
        self.assertEqual(match_body({"$size": 5, "$md5": "5d41402abc4b2a76b9719d911017c592"}, body, None), [])
        self.assertEqual(len(match_body({"$size": 6}, body, None)), 1)
        self.assertEqual(len(match_body({"$sha256": "nope"}, body, None)), 1)
        with self.assertRaises(MatchError):
            match_body({"$crc": "x"}, body, None)

    def test_content_descriptors(self):
        self.assertEqual(content_value("abc", None), b"abc")
        self.assertEqual(list(content_value({"$base64": "AQID"}, None)), [1, 2, 3])
        with self.assertRaises(MatchError):
            content_value({"$data": "x"}, None)
        with self.assertRaises(MatchError):
            content_value({"other": 1}, None)


if __name__ == "__main__":
    unittest.main()
