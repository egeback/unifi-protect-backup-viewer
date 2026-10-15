package indexer

import "testing"

// KAN-633: each unparseable file is warned about once per process, not on every scan.
func TestFirstUnparseableWarnsOnce(t *testing.T) {
	idx := &Indexer{}
	if !idx.firstUnparseable("a.mp4") {
		t.Fatal("first sighting should warn")
	}
	if idx.firstUnparseable("a.mp4") {
		t.Fatal("second sighting should not warn")
	}
	if !idx.firstUnparseable("b.mp4") {
		t.Fatal("another file should warn")
	}
}
