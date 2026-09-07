package s3tests

import (
	"slices"
	"strings"

	s3vectors "github.com/cloud-portable/s3vectors/packages/go"
)

// FilterFunc reports whether a vector should be selected. Custom selections
// are just functions; the package provides constructors for the common
// group/tag/id cases.
type FilterFunc func(*s3vectors.Vector) bool

// ApplyFilters returns the vectors selected by every filter (logical AND),
// preserving order. With no filters the input is returned unchanged. Exclude
// filters compose the same way: they return false for matching vectors, so
// ANDing drops them.
func ApplyFilters(vectors []*s3vectors.Vector, filters ...FilterFunc) []*s3vectors.Vector {
	if len(filters) == 0 {
		return vectors
	}
	var out []*s3vectors.Vector
	for _, v := range vectors {
		selected := true
		for _, f := range filters {
			if !f(v) {
				selected = false
				break
			}
		}
		if selected {
			out = append(out, v)
		}
	}
	return out
}

// Groups selects vectors in any of the given feature groups.
func Groups(groups ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool { return slices.Contains(groups, v.Group) }
}

// Tags selects vectors carrying at least one of the given tags.
func Tags(tags ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool {
		for _, t := range tags {
			if slices.Contains(v.Tags, t) {
				return true
			}
		}
		return false
	}
}

// IDs selects vectors with any of the given ids.
func IDs(ids ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool { return slices.Contains(ids, v.ID) }
}

// ExcludeGroups drops vectors in any of the given feature groups.
func ExcludeGroups(groups ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool { return !slices.Contains(groups, v.Group) }
}

// ExcludeTags drops vectors carrying any of the given tags.
func ExcludeTags(tags ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool {
		for _, t := range tags {
			if slices.Contains(v.Tags, t) {
				return false
			}
		}
		return true
	}
}

// ExcludeIDs drops vectors with any of the given ids. Dropped vectors leave
// no trace in results; to keep them visible as skipped, pass Skip to Run.
func ExcludeIDs(ids ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool { return !slices.Contains(ids, v.ID) }
}

// TagsMatching selects vectors carrying at least one tag matching any of the
// given glob patterns. A pattern uses '*' as a wildcard for any run of
// characters (including empty); every other character matches literally. Tags
// never contain '*', so a pattern without one is an exact match. Example:
// TagsMatching("quirk:*") selects every quirk-tagged vector.
func TagsMatching(patterns ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool { return anyTagMatches(v.Tags, patterns) }
}

// ExcludeTagsMatching drops vectors carrying any tag matching any of the given
// glob patterns (see TagsMatching for the pattern syntax). Example:
// ExcludeTagsMatching("quirk:*") drops every quirk-tagged vector.
func ExcludeTagsMatching(patterns ...string) FilterFunc {
	return func(v *s3vectors.Vector) bool { return !anyTagMatches(v.Tags, patterns) }
}

func anyTagMatches(tags, patterns []string) bool {
	for _, t := range tags {
		for _, p := range patterns {
			if globMatch(p, t) {
				return true
			}
		}
	}
	return false
}

// globMatch reports whether s matches a glob pattern whose only metacharacter
// is '*' (any run of characters, including empty); every other character is
// literal. Kept identical across the go/js/python runners.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return s == pattern // no wildcard: exact match
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
