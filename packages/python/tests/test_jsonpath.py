import unittest

from cloud_portable_s3tests._jsonpath import PathError, get, get_string

DOC = {
    "UploadId": "UP1",
    "ETag": '"abc"',
    "Contents": [{"Key": "a", "Size": 5}, {"Key": "b"}],
    "CopyPartResult": {"ETag": '"xyz"'},
    "headers": {"etag": '"h"'},
    "status": 200,
    "Deep": [["x"]],
}


class TestJsonPath(unittest.TestCase):
    def test_get_resolves_paths(self):
        for path, want in [
            ("UploadId", "UP1"),
            ("Contents[0].Key", "a"),
            ("Contents[1].Key", "b"),
            ("Contents[0].Size", 5),
            ("CopyPartResult.ETag", '"xyz"'),
            ("headers.etag", '"h"'),
            ("status", 200),
            ("Deep[0][0]", "x"),
        ]:
            self.assertEqual(get(DOC, path), want, path)

    def test_get_rejects_bad_paths(self):
        for path in [
            "", "Nope", "Contents[2].Key", "Contents[0].Nope", "UploadId.Sub",
            "Contents[x]", "Contents[", "Contents]", ".Leading", "Trailing.", "Contents[-1]",
        ]:
            with self.assertRaises(PathError, msg=path):
                get(DOC, path)

    def test_get_string_renders_scalars(self):
        self.assertEqual(get_string(DOC, "Contents[0].Size"), "5")
        self.assertEqual(get_string(DOC, "UploadId"), "UP1")
        self.assertEqual(get_string({"b": True, "f": 2.0}, "b"), "true")
        self.assertEqual(get_string({"b": True, "f": 2.0}, "f"), "2")
        with self.assertRaises(PathError):
            get_string(DOC, "Contents")


if __name__ == "__main__":
    unittest.main()
