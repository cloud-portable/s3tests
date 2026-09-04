import unittest

from cloud_portable_s3tests._interp import InterpError, Scope


def scope():
    def data(name, field):
        if name == "big" and field == "md5":
            return "deadbeef"
        raise KeyError("no such dataset/field")

    return Scope(
        env={"endpoint": "http://localhost:9000", "region": "us-east-1"},
        res={"b1": {"name": "bucket-x"}, "o1": {"etag": '"abc"', "versionId": "v1"}},
        cap={"uploadId": "UP123", "etag1": '"e1"'},
        data=data,
    )


class TestInterp(unittest.TestCase):
    def test_string_interpolation(self):
        sc = scope()
        for inp, want in [
            ("plain", "plain"),
            ("${res.b1.name}", "bucket-x"),
            ("prefix-${res.b1.name}-suffix", "prefix-bucket-x-suffix"),
            ("${cap.uploadId}", "UP123"),
            ("${env.endpoint}", "http://localhost:9000"),
            ("${data.big.md5}", "deadbeef"),
            ("$${res.b1.name}", "${res.b1.name}"),  # escaped
            ("cost: $5", "cost: $5"),  # bare $ is literal
            ("a$$b", "a$$b"),
            ("${res.b1.name}${cap.etag1}", 'bucket-x"e1"'),
        ]:
            self.assertEqual(sc.string(inp), want, inp)

    def test_unresolvable_placeholders_raise(self):
        sc = scope()
        for inp in [
            "${res.missing.name}", "${res.b1.nope}", "${cap.nope}", "${env.secret}",
            "${data.nope.md5}", "${data.big.nope}", "${bogus.x}", "${res.b1}", "${unclosed",
        ]:
            with self.assertRaises(InterpError, msg=inp):
                sc.string(inp)

    def test_value_interpolation_rebuilds_without_mutating(self):
        sc = scope()
        inp = {"Bucket": "${res.b1.name}", "Key": "k", "PartNumber": 1, "Nested": {"A": ["${cap.uploadId}", 5, True, None]}}
        out = sc.value(inp)
        self.assertEqual(out["Bucket"], "bucket-x")
        self.assertEqual(out["PartNumber"], 1)
        self.assertEqual(out["Nested"]["A"], ["UP123", 5, True, None])
        self.assertEqual(inp["Bucket"], "${res.b1.name}", "input must not be mutated")


if __name__ == "__main__":
    unittest.main()
