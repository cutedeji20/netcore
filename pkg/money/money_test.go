package money

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// §101 — the spec's own worked example.
func TestNairaKoboExample(t *testing.T) {
	a, err := ParseMinor("1500.00", "NGN")
	if err != nil {
		t.Fatal(err)
	}
	if a.Minor() != 150000 {
		t.Fatalf("₦1,500.00 = %d kobo, want 150000 (§101)", a.Minor())
	}
	if got := a.String(); got != "1500.00 NGN" {
		t.Errorf("String() = %q", got)
	}
}

func TestParseMinor(t *testing.T) {
	cases := []struct {
		in, cur string
		want    int64
	}{
		{"0", "NGN", 0},
		{"1", "NGN", 100},
		{"1.5", "NGN", 150},
		{"1.05", "NGN", 105},
		{"0.01", "NGN", 1},
		{"1500.00", "NGN", 150000},
		{"-25.50", "USD", -2550},
		{"1000", "JPY", 1000},  // zero-exponent currency
		{"1.500", "KWD", 1500}, // three-exponent currency
		{".50", "USD", 50},
	}
	for _, c := range cases {
		got, err := ParseMinor(c.in, c.cur)
		if err != nil {
			t.Errorf("ParseMinor(%q,%s) error: %v", c.in, c.cur, err)
			continue
		}
		if got.Minor() != c.want {
			t.Errorf("ParseMinor(%q,%s) = %d, want %d", c.in, c.cur, got.Minor(), c.want)
		}
	}
}

// Excess precision must be an error, never a silent round. Rounding money
// without saying so is how reconciliation drifts by a kobo per transaction.
func TestParseMinor_ExcessPrecisionRejected(t *testing.T) {
	if _, err := ParseMinor("1.005", "NGN"); err == nil {
		t.Fatal("expected rejection of 3 decimals for a 2-exponent currency")
	}
	if _, err := ParseMinor("1.5", "JPY"); err == nil {
		t.Fatal("expected rejection of any decimals for JPY")
	}
}

func TestNew_CurrencyValidation(t *testing.T) {
	for _, c := range []string{"ngn", "NGN", " ngn "} {
		a, err := New(100, c)
		if err != nil {
			t.Errorf("New(100,%q) error: %v", c, err)
			continue
		}
		if a.Currency() != "NGN" {
			t.Errorf("Currency() = %q, want NGN", a.Currency())
		}
	}
	for _, c := range []string{"", "N", "NG", "NGNN", "N1N", "12 "} {
		if _, err := New(100, c); !errors.Is(err, ErrInvalidCurrency) {
			t.Errorf("New(100,%q) should return ErrInvalidCurrency, got %v", c, err)
		}
	}
}

// Mixing currencies must be impossible, not merely discouraged.
func TestArithmetic_CurrencyMismatchRejected(t *testing.T) {
	ngn := MustNew(1000, "NGN")
	usd := MustNew(1000, "USD")

	if _, err := ngn.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Add across currencies: got %v, want ErrCurrencyMismatch", err)
	}
	if _, err := ngn.Sub(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Sub across currencies: got %v, want ErrCurrencyMismatch", err)
	}
	if ngn.Equal(usd) {
		t.Error("amounts of different currencies must not compare equal")
	}
}

func TestArithmetic(t *testing.T) {
	a := MustNew(150000, "NGN")
	b := MustNew(50000, "NGN")

	sum, err := a.Add(b)
	if err != nil || sum.Minor() != 200000 {
		t.Errorf("Add = %v (%v), want 200000", sum.Minor(), err)
	}
	diff, err := a.Sub(b)
	if err != nil || diff.Minor() != 100000 {
		t.Errorf("Sub = %v (%v), want 100000", diff.Minor(), err)
	}
}

func TestArithmetic_OverflowDetected(t *testing.T) {
	max := MustNew(1<<62, "NGN")
	if _, err := max.Add(max); !errors.Is(err, ErrOverflow) {
		t.Errorf("overflow not detected: %v", err)
	}
}

func TestString(t *testing.T) {
	cases := map[string]Amount{
		"1500.00 NGN": MustNew(150000, "NGN"),
		"0.01 NGN":    MustNew(1, "NGN"),
		"0.00 NGN":    MustNew(0, "NGN"),
		"-25.50 USD":  MustNew(-2550, "USD"),
		"1000 JPY":    MustNew(1000, "JPY"),
		"1.500 KWD":   MustNew(1500, "KWD"),
	}
	for want, a := range cases {
		if got := a.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"0.00", "1.00", "1500.00", "999999.99", "0.01"} {
		a, err := ParseMinor(s, "NGN")
		if err != nil {
			t.Fatal(err)
		}
		if got := a.String(); got != s+" NGN" {
			t.Errorf("round trip of %q gave %q", s, got)
		}
	}
}

// §102 — "add a lint rule rejecting float64 in money code". This is that rule,
// enforced as a test so it runs in every CI build without extra tooling.
func TestNoFloatingPointInThisPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	var found []string
	var scanned int

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if id.Name == "float64" || id.Name == "float32" {
				found = append(found, fset.Position(id.Pos()).String())
			}
			return true
		})
	}

	if scanned == 0 {
		t.Fatal("no source files scanned; the guard is not actually running")
	}
	if len(found) > 0 {
		t.Fatalf("§102 violation: floating point in money code at:\n  %s",
			strings.Join(found, "\n  "))
	}
}

func TestExponent(t *testing.T) {
	cases := map[string]int{"NGN": 2, "USD": 2, "EUR": 2, "JPY": 0, "KRW": 0, "KWD": 3, "BHD": 3}
	for cur, want := range cases {
		if got := MustNew(0, cur).Exponent(); got != want {
			t.Errorf("Exponent(%s) = %d, want %d", cur, got, want)
		}
	}
}

func TestIsZero(t *testing.T) {
	if !MustNew(0, "NGN").IsZero() {
		t.Error("zero amount should report IsZero")
	}
	if MustNew(1, "NGN").IsZero() {
		t.Error("non-zero amount should not report IsZero")
	}
}
