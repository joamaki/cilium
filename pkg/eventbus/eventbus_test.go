package eventbus

import (
	"testing"
)

type TestEventA struct{}

func (e *TestEventA) String() string {
	return "TestEventA"
}

type TestEventB struct{}

func (e *TestEventB) String() string {
	return "TestEventB"
}

type TestEventC struct{}

func (e *TestEventC) String() string {
	return "TestEventC"
}

func TestEventBus(t *testing.T) {
	builder := NewBuilder()

	pubA, err := builder.Register(new(TestEventA), "TestEventA")
	if err != nil {
		t.Fatal(err)
	}

	err = pubA(&TestEventA{})
	if err == nil {
		t.Fatal("expected publishing before bus was ready to fail")
	}

	pubB, err := builder.Register(new(TestEventB), "TestEventB")
	if err != nil {
		t.Fatal(err)
	}

	countA := 0
	unsubA := builder.Subscribe(
		new(TestEventA), "count TestEventA",
		func(ev Event) error {
			countA++
			return nil
		})

	countB := 0
	unsubB := builder.Subscribe(
		new(TestEventB), "count TestEventB",
		func(ev Event) error {
			countB++
			return nil
		})

	_, err = builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		err = pubA(&TestEventA{})
		if err != nil {
			t.Fatalf("expected pubA to succeed, got error: %s", err)
		}
	}
	for i := 0; i < 2; i++ {
		err = pubB(&TestEventB{})
		if err != nil {
			t.Fatalf("expected pubB to succeed, got error: %s", err)
		}
	}

	// Test with wrong type
	err = pubA(&TestEventB{})
	if err == nil {
		t.Fatal("expected pubA with TestEventB to fail")
	}

	if countA != 3 {
		t.Fatalf("expected countA=2, got %d", countA)
	}
	if countB != 2 {
		t.Fatalf("expected countB=2, got %d", countA)
	}

	unsubA()
	unsubB()

	err = pubA(&TestEventA{})
	if err != nil {
		t.Fatalf("expected pubA to succeed, got error: %s", err)
	}
	if countA != 3 {
		t.Fatalf("after unsubscribe, expected countA=2, got %d", countA)
	}

}

func BenchmarkEventBus(b *testing.B) {
	builder := NewBuilder()

	pubA, err := builder.Register(new(TestEventA), "source of EventA")
	if err != nil {
		b.Fatal(err)
	}

	count := 0
	_ = builder.Subscribe(
		new(TestEventA),
		"count EventAs",
		func(ev Event) error {
			count++
			return nil
		})
	if err != nil {
		b.Fatal(err)
	}

	_, err = builder.Build()
	if err != nil {
		b.Fatal(err)
	}

	ev := &TestEventA{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pubA(ev)
	}
	if count != b.N {
		b.Fatalf("missed events, expected %d, got %d", b.N, count)
	}
}

// BenchmarkFuncCall gives a baseline performance to which to compare
// BenchmarkGenbus to.
func BenchmarkFuncCall(b *testing.B) {
	count := 0

	var cb func(ev Event) error
	if b.N > 0 {
		// Hack to stop Go from inlining
		cb = func(ev Event) error {
			count++
			return nil
		}
	}

	ev := &TestEventA{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb(ev)
	}
	if count != b.N {
		b.Fatalf("missed events, expected %d, got %d", b.N, count)
	}
}
