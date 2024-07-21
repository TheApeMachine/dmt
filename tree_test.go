package dmt

import (
	"strconv"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type testCase struct {
	description string
	operations  []operation
	finalSize   int
	expectError bool
}

type operation struct {
	opType string
	key    string
	value  []byte
	expect interface{}
}

var testCases = []testCase{
	{
		description: "Insert single key-value pair",
		operations: []operation{
			{opType: "insert", key: "key1", value: []byte("value1")},
			{opType: "get", key: "key1", expect: []byte("value1")},
		},
		finalSize:   1,
		expectError: false,
	},
	{
		description: "Insert and delete key-value pair",
		operations: []operation{
			{opType: "insert", key: "key1", value: []byte("value1")},
			{opType: "delete", key: "key1"},
			{opType: "get", key: "key1", expect: nil},
		},
		finalSize:   0,
		expectError: true,
	},
	{
		description: "Insert multiple key-value pairs",
		operations: []operation{
			{opType: "insert", key: "key1", value: []byte("value1")},
			{opType: "insert", key: "key2", value: []byte("value2")},
			{opType: "get", key: "key1", expect: []byte("value1")},
			{opType: "get", key: "key2", expect: []byte("value2")},
		},
		finalSize:   2,
		expectError: false,
	},
	{
		description: "Insert duplicate key",
		operations: []operation{
			{opType: "insert", key: "key1", value: []byte("value1")},
			{opType: "insert", key: "key1", value: []byte("value2")},
			{opType: "get", key: "key1", expect: []byte("value2")},
		},
		finalSize:   1,
		expectError: false,
	},
	{
		description: "Get non-existent key",
		operations: []operation{
			{opType: "get", key: "key1", expect: nil},
		},
		finalSize:   0,
		expectError: true,
	},
}

func TestTreeOperations(t *testing.T) {
	Convey("Given a Tree", t, func() {
		for _, tc := range testCases {
			Convey(tc.description, func() {
				tree := NewTree()
				for _, op := range tc.operations {
					switch op.opType {
					case "insert":
						tree.Insert(op.key, op.value)
					case "get":
						value, found := tree.Get(op.key)
						if tc.expectError && value == nil {
							So(found, ShouldBeFalse)
							So(value, ShouldBeNil)
						} else {
							So(found, ShouldBeTrue)
							So(value, ShouldResemble, op.expect)
						}
					case "delete":
						tree.Delete(op.key)
					}
				}
				So(tree.Size(), ShouldEqual, tc.finalSize)
			})
		}
	})
}

func BenchmarkTreeInsert(b *testing.B) {
	tree := NewTree()
	for i := 0; i < b.N; i++ {
		tree.Insert("key"+strconv.Itoa(i), []byte("value"+strconv.Itoa(i)))
	}
}

func BenchmarkTreeGet(b *testing.B) {
	tree := NewTree()
	for i := 0; i < 1000; i++ {
		tree.Insert("key"+strconv.Itoa(i), []byte("value"+strconv.Itoa(i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.Get("key" + strconv.Itoa(i%1000))
	}
}

func BenchmarkTreeDelete(b *testing.B) {
	tree := NewTree()
	for i := 0; i < 1000; i++ {
		tree.Insert("key"+strconv.Itoa(i), []byte("value"+strconv.Itoa(i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.Delete("key" + strconv.Itoa(i%1000))
	}
}
