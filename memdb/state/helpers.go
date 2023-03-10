package state

type filterIterator[Obj any] struct {
	Iterator[Obj]
	keep func(obj Obj) bool
}

func (it filterIterator[Obj]) Next() (obj Obj, ok bool) {
	for {
		obj, ok = it.Iterator.Next()
		if !ok {
			return
		}
		if it.keep(obj) {
			return
		}
	}
}

func Filter[Obj any](it Iterator[Obj], keep func(obj Obj) bool) Iterator[Obj] {
	return filterIterator[Obj]{Iterator: it, keep: keep}
}

func Collect[Obj any](iter Iterator[Obj]) []Obj {
	out := make([]Obj, 0, 64)
	for obj, ok := iter.Next(); ok; obj, ok = iter.Next() {
		out = append(out, obj)
	}
	return out
}

func ProcessEach[Obj any](iter Iterator[Obj], fn func(Obj) error) (err error) {
	for obj, ok := iter.Next(); ok; obj, ok = iter.Next() {
		err = fn(obj)
		if err != nil {
			return
		}
	}
	return
}
