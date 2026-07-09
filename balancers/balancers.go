package balancers

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
)

type Balancer interface {
	Pop() (uint, error)
	Delete(key uint)
	Reduce(key uint)
	Success(key uint)
}

// 按权重概率抽取，类似抽签。
type Lottery struct {
	store   map[uint]int
	success uint
	fails   map[uint]struct{}
	reduces map[uint]struct{}
}

func NewLottery(items map[uint]int) *Lottery {
	store := make(map[uint]int, len(items))
	for key, weight := range items {
		if weight > 0 {
			store[key] = weight
		}
	}
	return &Lottery{
		store:   store,
		fails:   map[uint]struct{}{},
		reduces: map[uint]struct{}{},
	}
}

func (w *Lottery) Pop() (uint, error) {
	if len(w.store) == 0 {
		return 0, fmt.Errorf("no provide items or all items are disabled")
	}
	allowReduced := !w.hasUnreducedActive()
	total := 0
	for key, v := range w.store {
		if _, reduced := w.reduces[key]; reduced && !allowReduced {
			continue
		}
		total += v
	}
	if total <= 0 {
		return 0, fmt.Errorf("total provide weight must be greater than 0")
	}
	r := rand.IntN(total)
	for k, v := range w.store {
		if _, reduced := w.reduces[k]; reduced && !allowReduced {
			continue
		}
		if r < v {
			return k, nil
		}
		r -= v
	}
	return 0, fmt.Errorf("unexpected error")
}

func (w *Lottery) Delete(key uint) {
	w.fails[key] = struct{}{}
	delete(w.store, key)
}

func (w *Lottery) Reduce(key uint) {
	weight, ok := w.store[key]
	if !ok {
		return
	}
	w.reduces[key] = struct{}{}
	// 至少保留一个权重，确保所有提供商都降权时仍可继续尝试；
	// Pop 会优先选择未降权的提供商，因此权重为 1 时也能生效。
	if reduction := max(1, weight/3); weight > reduction {
		w.store[key] = weight - reduction
	}
}

func (w *Lottery) Success(key uint) {
	w.success = key
}

func (w *Lottery) hasUnreducedActive() bool {
	for key := range w.store {
		if _, reduced := w.reduces[key]; !reduced {
			return true
		}
	}
	return false
}

// 按权重展开后顺序轮转。
// 例如 A=2、B=1 时，跨请求序列为 A、A、B、A、A、B。
type Rotor struct {
	schedule []uint
	active   map[uint]struct{}
	stateKey string
	success  uint
	fails    map[uint]struct{}
	reduces  map[uint]struct{}
}

var (
	rotorMu      sync.Mutex
	rotorCursors = map[string]int{}
)

func NewRotor(items map[uint]int) *Rotor {
	entries := make([]struct {
		key    uint
		weight int
	}, 0, len(items))
	for key, weight := range items {
		if weight <= 0 {
			continue
		}
		entries = append(entries, struct {
			key    uint
			weight int
		}{key: key, weight: weight})
	}
	slices.SortFunc(entries, func(a, b struct {
		key    uint
		weight int
	}) int {
		if a.weight != b.weight {
			return b.weight - a.weight
		}
		if a.key < b.key {
			return -1
		}
		if a.key > b.key {
			return 1
		}
		return 0
	})

	schedule := make([]uint, 0)
	active := make(map[uint]struct{}, len(entries))
	for _, entry := range entries {
		active[entry.key] = struct{}{}
		for i := 0; i < entry.weight; i++ {
			schedule = append(schedule, entry.key)
		}
	}
	return &Rotor{
		schedule: schedule,
		active:   active,
		stateKey: rotorStateKey(entries),
		fails:    map[uint]struct{}{},
		reduces:  map[uint]struct{}{},
	}
}

func (w *Rotor) Pop() (uint, error) {
	if len(w.active) == 0 || len(w.schedule) == 0 {
		return 0, fmt.Errorf("no provide items")
	}

	allowReduced := !w.hasUnreducedActive()
	rotorMu.Lock()
	defer rotorMu.Unlock()
	cursor := rotorCursors[w.stateKey] % len(w.schedule)
	for checked := 0; checked < len(w.schedule); checked++ {
		key := w.schedule[cursor]
		cursor = (cursor + 1) % len(w.schedule)
		rotorCursors[w.stateKey] = cursor
		if _, ok := w.active[key]; !ok {
			continue
		}
		if _, reduced := w.reduces[key]; reduced && !allowReduced {
			continue
		}
		return key, nil
	}
	return 0, fmt.Errorf("no provide items")
}

func (w *Rotor) Delete(key uint) {
	w.fails[key] = struct{}{}
	delete(w.active, key)
	delete(w.reduces, key)
}

func (w *Rotor) Reduce(key uint) {
	w.reduces[key] = struct{}{}
}

func (w *Rotor) Success(key uint) {
	w.success = key
}

func (w *Rotor) hasUnreducedActive() bool {
	for key := range w.active {
		if _, reduced := w.reduces[key]; !reduced {
			return true
		}
	}
	return false
}

func rotorStateKey(entries []struct {
	key    uint
	weight int
}) string {
	if len(entries) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, fmt.Sprintf("%d:%d", entry.key, entry.weight))
	}
	return strings.Join(parts, ",")
}
