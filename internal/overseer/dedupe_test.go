package overseer

import (
	"testing"

	"github.com/maniginam/waggle/internal/model"
)

func item(key string) Item { return Item{Key: key, Event: &model.Event{TaskID: key}} }

func TestDeduperEmitsOnlyUnseen(t *testing.T) {
	d := newDeduper(10)
	first := d.filter([]Item{item("a"), item("b")})
	if len(first) != 2 {
		t.Fatalf("first pass = %d, want 2", len(first))
	}
	second := d.filter([]Item{item("a"), item("b"), item("c")})
	if len(second) != 1 || second[0].Key != "c" {
		t.Fatalf("second pass = %+v, want only c", second)
	}
}

func TestDeduperEvictsOldestPastCapacity(t *testing.T) {
	d := newDeduper(2)
	d.filter([]Item{item("a"), item("b")}) // seen: a,b
	d.filter([]Item{item("c")})            // adds c, evicts a; seen: b,c
	again := d.filter([]Item{item("a")})   // a was evicted -> emits again
	if len(again) != 1 || again[0].Key != "a" {
		t.Fatalf("got %+v, want a re-emitted", again)
	}
}
