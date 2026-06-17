package wasmloader

import (
	"sync"
	"testing"
)

// mustLoad loads a fixture and registers cleanup; fails the test on error.
func mustLoad(t *testing.T, path string, cbs ...*Callbacks) *Module {
	t.Helper()
	m, err := Load(path, cbs...)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func call(t *testing.T, m *Module, name string, args ...any) any {
	t.Helper()
	got, err := m.Call(name, args...)
	if err != nil {
		t.Fatalf("Call(%q, %v): %v", name, args, err)
	}
	return got
}

// SPEC §8 — math_50: numeric round-trip.
func TestMath(t *testing.T) {
	m := mustLoad(t, "tests/math_50.wasm")
	if got := call(t, m, "add", 3, 4); got != int32(7) {
		t.Errorf("add(3,4) = %v (%T); want int32(7)", got, got)
	}
	if got := call(t, m, "multiply", 2.5, 4.0); got != float64(10.0) {
		t.Errorf("multiply(2.5,4.0) = %v (%T); want float64(10)", got, got)
	}
	if got := call(t, m, "square", 5); got != int32(25) {
		t.Errorf("square(5) = %v (%T); want int32(25)", got, got)
	}
}

// SPEC §8 — booleans_50: bool normalization.
func TestBooleans(t *testing.T) {
	m := mustLoad(t, "tests/booleans_50.wasm")
	cases := []struct {
		name string
		args []any
		want bool
	}{
		{"isPositive", []any{1.0}, true},
		{"isPositive", []any{-1.0}, false},
		{"inRange", []any{5.0, 0.0, 10.0}, true},
		{"inRange", []any{11.0, 0.0, 10.0}, false},
		{"isEven", []any{4}, true},
		{"isEven", []any{3}, false},
	}
	for _, c := range cases {
		if got := call(t, m, c.name, c.args...); got != c.want {
			t.Errorf("%s(%v) = %v (%T); want %v", c.name, c.args, got, got, c.want)
		}
	}
}

// SPEC §8 — strings_50: string param + Canonical-ABI callee-allocated return.
func TestStrings(t *testing.T) {
	m := mustLoad(t, "tests/strings_50.wasm")
	if got := call(t, m, "greet", "World"); got != "Hello, World!" {
		t.Errorf("greet(\"World\") = %q; want %q", got, "Hello, World!")
	}
	if got := call(t, m, "shout", "hi"); got != "hihi" {
		t.Errorf("shout(\"hi\") = %q; want %q", got, "hihi")
	}
	if got := call(t, m, "strLen", "hello"); got != int32(5) {
		t.Errorf("strLen(\"hello\") = %v (%T); want int32(5)", got, got)
	}
}

// SPEC §8 — imports_50: host import callbacks.
func TestImports(t *testing.T) {
	cbs := NewCallbacks().
		On("envMul", func(a, b float64) float64 { return a * b }).
		On("envAdd", func(a, b int32) int32 { return a + b })
	m := mustLoad(t, "tests/imports_50.wasm", cbs)
	if got := call(t, m, "scale", 3.0, 4.0); got != float64(12.0) {
		t.Errorf("scale(3,4) = %v (%T); want float64(12)", got, got)
	}
	if got := call(t, m, "combine", 10, 7); got != int32(17) {
		t.Errorf("combine(10,7) = %v (%T); want int32(17)", got, got)
	}
}

// Pattern — Bind (destructure): a named export as a standalone callable.
func TestBind(t *testing.T) {
	m := mustLoad(t, "tests/math_50.wasm")
	add := m.Bind("add")
	square := m.Bind("square")
	if got, err := add(3, 4); err != nil || got != int32(7) {
		t.Errorf("bound add(3,4) = %v, %v; want 7", got, err)
	}
	if got, err := square(6); err != nil || got != int32(36) {
		t.Errorf("bound square(6) = %v, %v; want 36", got, err)
	}
}

// SPEC §6.1 — createSingleton returns the same instance on every call.
func TestSingleton(t *testing.T) {
	get := CreateSingleton("tests/math_50.wasm")
	m1, err := get()
	if err != nil {
		t.Fatalf("singleton first call: %v", err)
	}
	t.Cleanup(func() { _ = m1.Close() })
	m2, err := get()
	if err != nil {
		t.Fatalf("singleton second call: %v", err)
	}
	if m1 != m2 {
		t.Errorf("singleton returned different instances: %p vs %p", m1, m2)
	}
	if got := call(t, m1, "add", 1, 2); got != int32(3) {
		t.Errorf("singleton add(1,2) = %v; want 3", got)
	}
}

// SPEC §6.2 — InstancePool: Run returns the correct result; concurrent Runs all succeed.
func TestPool(t *testing.T) {
	pool := NewInstancePool("tests/math_50.wasm", 2)
	t.Cleanup(func() { _ = pool.Close() })

	got, err := pool.Run(func(m *Module) (any, error) { return m.Call("add", 20, 22) })
	if err != nil || got != int32(42) {
		t.Fatalf("pool.Run add(20,22) = %v, %v; want 42", got, err)
	}

	// Two concurrent Run calls on a size-2 pool must both complete without error.
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := pool.Run(func(m *Module) (any, error) { return m.Call("square", i) })
			if err != nil {
				errs <- err
				return
			}
			if r != int32(i*i) {
				errs <- &mismatch{i, r}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

type mismatch struct {
	i   int
	got any
}

func (m *mismatch) Error() string {
	return "square(" + itoa(m.i) + ") concurrent result wrong"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
