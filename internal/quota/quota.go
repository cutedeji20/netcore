// Package quota implements data-cap accounting.
//
// Spec: BUILD.md §21A.
//
// This package exists as its own module (§106) because quota logic is invoked
// from payments (period creation), subscriptions (reset on renewal),
// accounting (decrement), and RADIUS (read). Scattering it across four
// packages is how the §21A.8 invariant gets violated by someone who did not
// know it existed.
//
// The invariant, restated:
//
//	consumed_bytes is monotonically non-decreasing, and applying the same
//	accounting packet twice has the same effect as applying it once.
//
// The durable half of that invariant lives in the quota_apply() SQL function
// (migration 0002). This package owns the pure logic: octet reassembly,
// session budgeting, and exhaustion policy.
package quota

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ---------------------------------------------------------------------------
// §21A.6 — Gigawords
// ---------------------------------------------------------------------------

// MaxOctets32 is the wrap point of a 32-bit RADIUS octet counter: 4 GiB.
const MaxOctets32 = uint64(1) << 32

// TotalOctets reassembles a 64-bit byte count from the 32-bit RADIUS counter
// and its Gigawords high word.
//
// Acct-Input-Octets and Acct-Output-Octets are 32-bit unsigned and wrap at
// 4 GiB. RFC 2869 defines Acct-Input-Gigawords / Acct-Output-Gigawords to
// carry the number of wraps.
//
// This is the ONLY correct way to read octets. Skip it and a customer on a
// 50 GB plan is billed for 50 mod 4 = 2 GB — silently, and invisibly in
// testing, because nobody test-downloads 4 GB.
func TotalOctets(octets, gigawords uint32) uint64 {
	return uint64(gigawords)<<32 | uint64(octets)
}

// SessionUsage is one accounting packet's view of a session's cumulative
// traffic. Values are cumulative for the session, not deltas.
type SessionUsage struct {
	InputOctets     uint32
	OutputOctets    uint32
	InputGigawords  uint32
	OutputGigawords uint32
}

// Total returns the session's cumulative bytes in both directions.
//
// ISP data caps conventionally count upload plus download. If a tenant needs
// download-only metering, that is a plan-level policy decision and belongs in
// the plan model, not here.
func (u SessionUsage) Total() uint64 {
	return TotalOctets(u.InputOctets, u.InputGigawords) +
		TotalOctets(u.OutputOctets, u.OutputGigawords)
}

// ---------------------------------------------------------------------------
// §21A.3 — session budgeting
// ---------------------------------------------------------------------------

// ExhaustedAction is what happens when a subscription's pool reaches zero.
type ExhaustedAction string

const (
	// ActionThrottle keeps the customer online at reduced speed. This is the
	// recommended default (§21A.3): a hard disconnect at 23:00 generates a
	// support ticket, a throttle generates a top-up.
	ActionThrottle ExhaustedAction = "THROTTLE"
	// ActionDisconnect rejects authorization outright.
	ActionDisconnect ExhaustedAction = "DISCONNECT"
	// ActionRedirect admits the session into a walled garden pointing at the
	// top-up page.
	ActionRedirect ExhaustedAction = "REDIRECT"
)

// BudgetPolicy governs how much of a remaining pool a single session may claim.
type BudgetPolicy struct {
	// MaxPerSession caps any one session's budget so a session that fails to
	// report cannot consume the whole balance. Default 2 GiB.
	MaxPerSession uint64
	// FractionDenominator expresses "at most 1/N of remaining". Default 4 (25%).
	FractionDenominator uint64
	// MinPerSession is a floor.
	//
	// Without it, "min(2 GiB, 25% of remaining)" has an asymptote: 100 MiB
	// remaining yields 25 MiB, then 18.75 MiB, then 14 MiB... The tail of a
	// prepaid bundle can never be consumed and the customer reconnects in a
	// loop. Below this floor, grant the entire remainder.
	MinPerSession uint64
}

// DefaultBudgetPolicy returns the §21A.3 starting values, including the
// v1.2 floor.
func DefaultBudgetPolicy() BudgetPolicy {
	return BudgetPolicy{
		MaxPerSession:       2 << 30,  // 2 GiB
		FractionDenominator: 4,        // 25%
		MinPerSession:       32 << 20, // 32 MiB
	}
}

// Counter is the persisted quota state for one subscription period.
type Counter struct {
	QuotaBytes    uint64
	ConsumedBytes uint64
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Unmetered     bool
}

// Remaining returns the unconsumed balance, floored at zero.
func (c Counter) Remaining() uint64 {
	if c.Unmetered {
		return math.MaxUint64
	}
	if c.ConsumedBytes >= c.QuotaBytes {
		return 0
	}
	return c.QuotaBytes - c.ConsumedBytes
}

// Exhausted reports whether the pool is spent.
func (c Counter) Exhausted() bool {
	return !c.Unmetered && c.ConsumedBytes >= c.QuotaBytes
}

// SessionBudget computes the byte budget to hand a session at Access-Accept,
// per §21A.3.
//
// Returns 0 when the pool is exhausted; the caller then applies the plan's
// ExhaustedAction. An unmetered counter returns 0 with ok=false to signal
// "do not send a Total-Limit attribute at all" — sending a limit of zero to
// RouterOS would cut the customer off immediately.
func SessionBudget(c Counter, p BudgetPolicy) (budget uint64, metered bool) {
	if c.Unmetered {
		return 0, false
	}
	remaining := c.Remaining()
	if remaining == 0 {
		return 0, true
	}
	if p.FractionDenominator == 0 {
		p.FractionDenominator = 1
	}

	b := remaining / p.FractionDenominator
	if p.MaxPerSession > 0 && b > p.MaxPerSession {
		b = p.MaxPerSession
	}
	// v1.2 floor: below MinPerSession, grant the whole remainder rather than
	// slicing it into un-consumable fragments.
	if b < p.MinPerSession {
		b = remaining
	}
	if b > remaining {
		b = remaining
	}
	return b, true
}

// SplitGigawords decomposes a byte budget into the low and high 32-bit words
// required by Mikrotik-Total-Limit and Mikrotik-Total-Limit-Gigawords.
//
// Note the boundary case: a budget that is an exact multiple of 2^32 yields a
// low word of zero. RouterOS treats Total-Limit=0 as "no traffic allowed", so
// callers must not emit a bare zero low word — Clamp handles this.
func SplitGigawords(budget uint64) (low, high uint32) {
	return uint32(budget & 0xFFFFFFFF), uint32(budget >> 32)
}

// ClampBudget adjusts a budget so its low word is never zero, avoiding the
// RouterOS "Total-Limit=0 means no access" boundary. Subtracting one byte from
// a multi-gigabyte budget is not a meaningful loss to the customer.
func ClampBudget(budget uint64) uint64 {
	if budget == 0 {
		return 0
	}
	if budget&0xFFFFFFFF == 0 {
		return budget - 1
	}
	return budget
}

// ---------------------------------------------------------------------------
// §21A.5/§21A.8 — applying accounting
// ---------------------------------------------------------------------------

var (
	// ErrNoCounter means an accounting packet arrived for a period with no
	// counter row. This is NOT a benign no-op: real traffic has nowhere to be
	// recorded. Callers must surface it as quota_counter_missing_total (P2),
	// never swallow it.
	ErrNoCounter = errors.New("quota: no counter row for packet period")
	// ErrRegression means a session reported a cumulative total lower than a
	// previously applied one by more than the tolerated jitter.
	ErrRegression = errors.New("quota: cumulative usage regressed")
)

// Delta computes the billable increment for a packet given the session's
// previously applied high-water mark.
//
// Replayed and out-of-order packets produce a zero delta. This mirrors the
// SQL in quota_apply(); it exists here so callers can decide whether to issue
// a database round-trip at all, and so the logic is unit-testable without a
// database.
func Delta(cumulative, watermark uint64) uint64 {
	if cumulative <= watermark {
		return 0
	}
	return cumulative - watermark
}

// InterimInterval returns the Acct-Interim-Interval to advertise for a
// session, per §21A.5.
//
// Base interval everywhere; tightened to fine when the remaining budget could
// be consumed at line rate within one base interval. Precision where it
// matters, cheap everywhere else.
//
// Never returns below 60s: sub-minute interim updates are an accounting flood.
func InterimInterval(budget uint64, lineRateBps uint64, base, fine time.Duration) time.Duration {
	const floor = 60 * time.Second
	if base < floor {
		base = floor
	}
	if fine < floor {
		fine = floor
	}
	if lineRateBps == 0 || budget == 0 {
		return base
	}
	bytesPerSec := lineRateBps / 8
	if bytesPerSec == 0 {
		return base
	}
	secondsToBurn := budget / bytesPerSec
	if secondsToBurn <= uint64(base.Seconds()) {
		return fine
	}
	return base
}

// StaleAfter returns how long a session may go without an interim update
// before the reaper treats it as dead (§24A.3).
//
// Three consecutive missed updates plus a grace period. Three missed updates
// is not a slow network; it is a dead session.
func StaleAfter(interim time.Duration, grace time.Duration) time.Duration {
	return 3*interim + grace
}

// ---------------------------------------------------------------------------
// §21A.7 — reset periods
// ---------------------------------------------------------------------------

// ResetPolicy determines when a quota period rolls over.
type ResetPolicy string

const (
	ResetNone            ResetPolicy = "NONE"
	ResetPerSubscription ResetPolicy = "PER_SUBSCRIPTION"
	ResetDaily           ResetPolicy = "DAILY"
	ResetMonthly         ResetPolicy = "MONTHLY"
)

// NextPeriod computes the [start, end) window containing at, expressed in UTC
// but with boundaries computed in the tenant's location (§21A.7, §100).
//
// Storing UTC and computing boundaries locally is the whole point: Lagos is
// UTC+1 year-round, which makes this trivial today and a latent bug the day a
// tenant in a DST jurisdiction is onboarded.
func NextPeriod(policy ResetPolicy, at time.Time, loc *time.Location, subStart, subEnd time.Time) (start, end time.Time, err error) {
	if loc == nil {
		loc = time.UTC
	}
	local := at.In(loc)

	switch policy {
	case ResetNone, ResetPerSubscription:
		if subStart.IsZero() || subEnd.IsZero() {
			return time.Time{}, time.Time{}, fmt.Errorf("quota: policy %s requires subscription bounds", policy)
		}
		return subStart.UTC(), subEnd.UTC(), nil

	case ResetDaily:
		s := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		return s.UTC(), s.AddDate(0, 0, 1).UTC(), nil

	case ResetMonthly:
		// Anniversary-day monthly, not calendar-month, so a subscription
		// bought on the 15th resets on the 15th.
		if subStart.IsZero() {
			s := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
			return s.UTC(), s.AddDate(0, 1, 0).UTC(), nil
		}
		day := subStart.In(loc).Day()

		// Month arithmetic must be done on (year, month) pairs, never with
		// AddDate on a clamped date: Jan 31 + 1 month normalizes to Mar 3 in
		// Go, which silently skips February and produces a period that both
		// starts and ends in the wrong month.
		y, m := local.Year(), local.Month()
		s := clampedDate(y, m, day, loc)
		if local.Before(s) {
			y, m = shiftMonth(y, m, -1)
			s = clampedDate(y, m, day, loc)
		}
		ey, em := shiftMonth(s.Year(), s.Month(), 1)
		e := clampedDate(ey, em, day, loc)
		return s.UTC(), e.UTC(), nil

	default:
		return time.Time{}, time.Time{}, fmt.Errorf("quota: unknown reset policy %q", policy)
	}
}

// shiftMonth moves a (year, month) pair by delta months without touching a
// day-of-month, so no normalization can occur.
func shiftMonth(year int, month time.Month, delta int) (int, time.Month) {
	n := int(month) - 1 + delta
	year += n / 12
	n %= 12
	if n < 0 {
		n += 12
		year--
	}
	return year, time.Month(n + 1)
}

// clampedDate builds a date, clamping the day to the last day of the month.
// A subscription anchored on the 31st must still reset in February.
func clampedDate(year int, month time.Month, day int, loc *time.Location) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}
