package dmt

import (
	"strings"
)

// PrefixPermutator is responsible for generating permutations of a given prefix.
type PrefixPermutator struct {
	original string
}

// NewPrefixPermutator creates a new instance of PrefixPermutator.
func NewPrefixPermutator(prefix string) *PrefixPermutator {
	return &PrefixPermutator{
		original: prefix,
	}
}

// Permute generates all ordered combinations of the prefix segments.
func (p *PrefixPermutator) Permute() []string {
	if p.original == "" {
		return []string{""}
	}

	prefixParts := strings.Split(p.original, "/")
	permutations := permute(prefixParts)

	result := make([]string, len(permutations))
	for i, perm := range permutations {
		result[i] = strings.Join(perm, "/")
	}

	return result
}

// permute generates all permutations of the input slice.
func permute(input []string) [][]string {
	var result [][]string

	if len(input) == 0 {
		return result
	}

	var helper func([]string, int)
	helper = func(arr []string, n int) {
		if n == 1 {
			tmp := make([]string, len(arr))
			copy(tmp, arr)
			result = append(result, tmp)
			return
		}

		for i := 0; i < n; i++ {
			helper(arr, n-1)
			if n%2 == 1 {
				arr[0], arr[n-1] = arr[n-1], arr[0]
				continue
			}
			arr[i], arr[n-1] = arr[n-1], arr[i]
		}
	}

	helper(input, len(input))
	return result
}
