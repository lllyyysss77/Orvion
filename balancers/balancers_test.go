package balancers

import "testing"

func TestRotorWeightedSequencePersistsAcrossInstances(t *testing.T) {
	items := map[uint]int{
		101: 2,
		202: 1,
	}
	resetRotorCursorForTest(items)

	expected := []uint{101, 101, 202, 101, 101, 202}
	for index, want := range expected {
		got, err := NewRotor(items).Pop()
		if err != nil {
			t.Fatalf("第 %d 次 Pop 返回错误: %v", index+1, err)
		}
		if got != want {
			t.Fatalf("第 %d 次 Pop 期望 %d，实际 %d", index+1, want, got)
		}
	}
}

func TestRotorReduceSkipsReducedProviderWhenOtherProvidersAvailable(t *testing.T) {
	items := map[uint]int{
		301: 2,
		402: 1,
	}
	resetRotorCursorForTest(items)

	rotor := NewRotor(items)
	first, err := rotor.Pop()
	if err != nil {
		t.Fatalf("第一次 Pop 返回错误: %v", err)
	}
	if first != 301 {
		t.Fatalf("第一次 Pop 期望命中高权重提供商，实际 %d", first)
	}

	rotor.Reduce(first)
	second, err := rotor.Pop()
	if err != nil {
		t.Fatalf("第二次 Pop 返回错误: %v", err)
	}
	if second != 402 {
		t.Fatalf("Reduce 后应优先切到其它提供商，实际 %d", second)
	}
}

func resetRotorCursorForTest(items map[uint]int) {
	rotorMu.Lock()
	defer rotorMu.Unlock()
	delete(rotorCursors, NewRotor(items).stateKey)
}
