import unittest

from cloud_portable_s3tests._skip import skip_reason, default_skip, skip
from cloud_portable_s3tests._filter import tags, tags_matching, ids


def _is_skipped(v, extra=(), no_skip=(), no_skip_matching=()):
    """Mirror Runner.run's skip decision: the default quirk rule is prepended
    before the caller's rules, then a no_skip/no_skip_matching unskip opts
    vectors back in."""
    reason = skip_reason([default_skip, *extra], v)
    unskip = [tags(*no_skip), tags_matching(*no_skip_matching)]
    return reason is not None and not any(u(v) for u in unskip)


class TestDefaultQuirkSkip(unittest.TestCase):
    def test_default_and_opt_in(self):
        quirk = {"id": "a", "tags": ["tier-1", "quirk:not-aws"]}
        dir_ = {"id": "b", "tags": ["quirk:directory-bucket"]}
        plain = {"id": "c", "tags": ["tier-1", "listing"]}

        # default: quirks skipped, ordinary vectors run
        self.assertTrue(_is_skipped(quirk))
        self.assertTrue(_is_skipped(dir_))
        self.assertFalse(_is_skipped(plain))

        # no_skip exact opts one tag back in, leaving other quirks skipped
        self.assertFalse(_is_skipped(dir_, no_skip=["quirk:directory-bucket"]))
        self.assertTrue(_is_skipped(quirk, no_skip=["quirk:directory-bucket"]))

        # no_skip_matching glob opts every quirk back in
        self.assertFalse(_is_skipped(quirk, no_skip_matching=["quirk:*"]))
        self.assertFalse(_is_skipped(dir_, no_skip_matching=["quirk:*"]))

    def test_unskips_explicit_skip_too(self):
        flaky = {"id": "d", "tags": ["flaky"]}
        plain = {"id": "c", "tags": ["listing"]}
        rule = skip("flaky here", tags_matching("flaky"))
        self.assertTrue(_is_skipped(flaky, extra=[rule]))
        self.assertFalse(_is_skipped(flaky, extra=[rule], no_skip=["flaky"]))
        self.assertTrue(_is_skipped(plain, extra=[skip("known bug", ids("c"))]))


if __name__ == "__main__":
    unittest.main()
