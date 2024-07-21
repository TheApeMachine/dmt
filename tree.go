package dmt

import (
	iradix "github.com/hashicorp/go-immutable-radix/v2"
)

type Tree struct {
	trie *iradix.Tree[[]byte]
}

func NewTree() *Tree {
	return &Tree{
		trie: iradix.New[[]byte](),
	}
}

func (t *Tree) Insert(key string, value []byte) {
	t.trie, _, _ = t.trie.Insert([]byte(key), value)
}

func (t *Tree) Get(key string) ([]byte, bool) {
	value, found := t.trie.Get([]byte(key))
	return value, found
}

func (t *Tree) Delete(key string) {
	t.trie, _, _ = t.trie.Delete([]byte(key))
}

func (t *Tree) Size() int {
	return t.trie.Len()
}
