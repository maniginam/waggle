package overseer

// deduper tracks recently-seen stable keys so unchanged state is not re-emitted.
// Bounded so it cannot grow without limit; evicts oldest first.
type deduper struct {
	seen  map[string]struct{}
	order []string
	cap   int
}

func newDeduper(capacity int) *deduper {
	if capacity < 1 {
		capacity = 1
	}
	return &deduper{seen: make(map[string]struct{}), cap: capacity}
}

func (d *deduper) filter(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if _, ok := d.seen[it.Key]; ok {
			continue
		}
		d.seen[it.Key] = struct{}{}
		d.order = append(d.order, it.Key)
		out = append(out, it)
		if len(d.order) > d.cap {
			oldest := d.order[0]
			copy(d.order, d.order[1:])
			d.order = d.order[:d.cap]
			delete(d.seen, oldest)
		}
	}
	return out
}
