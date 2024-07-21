package dmt

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type permutationTestCase struct {
	description string
	prefix      string
	expected    []string
}

var permutationTestCases = []permutationTestCase{
	{
		description: "Permute simple prefix",
		prefix:      "a/b/c",
		expected:    []string{"a/b/c", "b/a/c", "c/b/a", "a/c/b", "b/c/a", "c/a/b"},
	},
	{
		description: "Permute single character",
		prefix:      "a",
		expected:    []string{"a"},
	},
	{
		description: "Permute empty string",
		prefix:      "",
		expected:    []string{""},
	},
	{
		description: "Permute two segments",
		prefix:      "a/b",
		expected:    []string{"a/b", "b/a"},
	},
}

func TestPrefixPermutator(t *testing.T) {
	Convey("Given a PrefixPermutator", t, func() {
		for _, tc := range permutationTestCases {
			Convey(tc.description, func() {
				permutator := NewPrefixPermutator(tc.prefix)
				permutations := permutator.Permute()

				// Verify the count of permutations
				So(len(permutations), ShouldEqual, len(tc.expected))

				// Ensure each permutation is unique
				uniquePermutations := make(map[string]bool)
				for _, perm := range permutations {
					uniquePermutations[perm] = true
				}
				So(len(uniquePermutations), ShouldEqual, len(permutations))

				// Convert slices to maps to ignore order and compare
				expectedMap := sliceToMap(tc.expected)
				actualMap := sliceToMap(permutations)
				So(actualMap, ShouldResemble, expectedMap)
			})
		}
	})
}

// Helper function to convert slice to map for comparison
func sliceToMap(slice []string) map[string]bool {
	m := make(map[string]bool, len(slice))
	for _, item := range slice {
		m[item] = true
	}
	return m
}

func BenchmarkPrefixPermutatorShort(b *testing.B) {
	prefix := "a/b/c"
	perm := NewPrefixPermutator(prefix)
	for i := 0; i < b.N; i++ {
		perm.Permute()
	}
}

func BenchmarkPrefixPermutatorMedium(b *testing.B) {
	prefix := "a/b/c/d/e"
	perm := NewPrefixPermutator(prefix)
	for i := 0; i < b.N; i++ {
		perm.Permute()
	}
}

func BenchmarkPrefixPermutatorLong(b *testing.B) {
	prefix := "a/b/c/d/e/f/g/h/i/j"
	perm := NewPrefixPermutator(prefix)
	for i := 0; i < b.N; i++ {
		perm.Permute()
	}
}
