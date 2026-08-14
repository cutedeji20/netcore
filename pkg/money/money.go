// Package money implements integer minor-unit currency handling.
//
// Spec: BUILD.md §101, §102.
//
// There is no float64 in this package and there must never be one. Currency
// amounts are stored, transported, and computed as integer minor units
// (kobo for NGN, cents for USD). ₦1,500.00 is 150000, always.
//
// Paystack already transacts in kobo, so the gateway boundary needs no
// conversion — see §18A.1.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Amount is a monetary value in integer minor units, tagged with its currency.
// The zero value is not valid: it has no currency. Construct with New.
type Amount struct {
	minor    int64
	currency string
}

var (
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	ErrInvalidCurrency  = errors.New("money: currency must be a 3-letter ISO code")
	ErrNegative         = errors.New("money: amount must not be negative")
	ErrOverflow         = errors.New("money: arithmetic overflow")
)

// minorUnitExponent is the number of decimal places for currencies whose
// exponent is not 2. Currencies absent from this map are assumed to use 2.
//
// Getting this wrong by a factor of 100 is a classic billing incident, so
// exceptions are enumerated rather than guessed.
var minorUnitExponent = map[string]int{
	"JPY": 0, "KRW": 0, "VND": 0, "CLP": 0, "ISK": 0, "XAF": 0, "XOF": 0,
	"BHD": 3, "KWD": 3, "OMR": 3, "TND": 3, "JOD": 3,
}

// New constructs an Amount from minor units. Currency is normalized to upper case.
func New(minor int64, currency string) (Amount, error) {
	c := strings.ToUpper(strings.TrimSpace(currency))
	if len(c) != 3 {
		return Amount{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	for i := 0; i < len(c); i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return Amount{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
		}
	}
	return Amount{minor: minor, currency: c}, nil
}

// MustNew is New for constants and tests. It panics on invalid input.
func MustNew(minor int64, currency string) Amount {
	a, err := New(minor, currency)
	if err != nil {
		panic(err)
	}
	return a
}

// Minor returns the raw minor-unit value. This is what goes in the database
// and what goes to the payment gateway.
func (a Amount) Minor() int64 { return a.minor }

// Currency returns the ISO 4217 code.
func (a Amount) Currency() string { return a.currency }

// IsZero reports whether the amount is zero. It does not consider currency.
func (a Amount) IsZero() bool { return a.minor == 0 }

// Exponent returns the number of minor-unit decimal places for the currency.
func (a Amount) Exponent() int {
	if e, ok := minorUnitExponent[a.currency]; ok {
		return e
	}
	return 2
}

// Add returns a+b. Both operands must share a currency.
func (a Amount) Add(b Amount) (Amount, error) {
	if err := a.assertSameCurrency(b); err != nil {
		return Amount{}, err
	}
	sum := a.minor + b.minor
	// Overflow detection: signs of operands equal, sign of result differs.
	if (a.minor > 0 && b.minor > 0 && sum < 0) || (a.minor < 0 && b.minor < 0 && sum > 0) {
		return Amount{}, ErrOverflow
	}
	return Amount{minor: sum, currency: a.currency}, nil
}

// Sub returns a-b. Both operands must share a currency.
func (a Amount) Sub(b Amount) (Amount, error) {
	if err := a.assertSameCurrency(b); err != nil {
		return Amount{}, err
	}
	d := a.minor - b.minor
	if (a.minor >= 0 && b.minor < 0 && d < 0) || (a.minor < 0 && b.minor > 0 && d > 0) {
		return Amount{}, ErrOverflow
	}
	return Amount{minor: d, currency: a.currency}, nil
}

// Equal reports whether two amounts are identical in value and currency.
func (a Amount) Equal(b Amount) bool {
	return a.minor == b.minor && a.currency == b.currency
}

func (a Amount) assertSameCurrency(b Amount) error {
	if a.currency != b.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, a.currency, b.currency)
	}
	return nil
}

// String renders the amount for humans and logs. It is NOT a serialization
// format — persist and transmit Minor() and Currency() separately (§102).
func (a Amount) String() string {
	exp := a.Exponent()
	neg := a.minor < 0
	v := a.minor
	if neg {
		v = -v
	}
	s := strconv.FormatInt(v, 10)
	if exp == 0 {
		if neg {
			return "-" + s + " " + a.currency
		}
		return s + " " + a.currency
	}
	for len(s) <= exp {
		s = "0" + s
	}
	out := s[:len(s)-exp] + "." + s[len(s)-exp:]
	if neg {
		out = "-" + out
	}
	return out + " " + a.currency
}

// ParseMinor parses a decimal string ("1500.00") into minor units for the
// given currency. Used only at trusted boundaries such as configuration and
// admin input — never for gateway amounts, which already arrive in minor units.
func ParseMinor(s, currency string) (Amount, error) {
	probe, err := New(0, currency)
	if err != nil {
		return Amount{}, err
	}
	exp := probe.Exponent()

	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		intPart = "0"
	}
	if !hasFrac {
		fracPart = ""
	}
	if len(fracPart) > exp {
		return Amount{}, fmt.Errorf("money: %q has more than %d decimal places for %s", s, exp, currency)
	}
	for len(fracPart) < exp {
		fracPart += "0"
	}

	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return Amount{}, fmt.Errorf("money: bad integer part in %q: %w", s, err)
	}
	var frac int64
	if exp > 0 {
		frac, err = strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return Amount{}, fmt.Errorf("money: bad fractional part in %q: %w", s, err)
		}
	}

	mult := int64(1)
	for i := 0; i < exp; i++ {
		mult *= 10
	}
	total := whole*mult + frac
	if neg {
		total = -total
	}
	return Amount{minor: total, currency: probe.currency}, nil
}
