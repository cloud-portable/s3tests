import unittest

from cloud_portable_s3vectors.datagen import DERIVED_FIELDS, derived

from cloud_portable_s3tests._vdata import DataCache, DataError

SPECS = {
    "big": {"$prng": {"seed": "test-0001/big", "size": 100000}},
    "part1": {"$slice": {"of": "big", "offset": 0, "length": 50000}},
    "aaa": {"$pattern": {"pattern": "A", "size": 16}},
}


class TestVData(unittest.TestCase):
    def test_derived_values_match_the_datagen_reference(self):
        cache = DataCache(SPECS)
        for name in ["big", "part1", "aaa"]:
            for field in DERIVED_FIELDS:
                self.assertEqual(cache.derived(name, field), derived(SPECS, name, field), f"{name}.{field}")

    def test_bytes_are_memoized(self):
        cache = DataCache(SPECS)
        self.assertIs(cache.bytes("big"), cache.bytes("big"))

    def test_errors_on_unknown_datasets_and_fields(self):
        cache = DataCache(SPECS)
        with self.assertRaises(DataError):
            cache.bytes("nope")
        with self.assertRaises(DataError):
            cache.derived("big", "nope")


if __name__ == "__main__":
    unittest.main()
