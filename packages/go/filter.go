package s3tests

import (
	"slices"

	s3vectors "github.com/alanshaw/s3vectors/packages/go"
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
