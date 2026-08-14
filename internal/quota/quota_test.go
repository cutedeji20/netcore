package quota

import (
	"math"
	"sync"
	"testing"
	"time"
)

// §21A.6 — the required test. This exact case is named in the spec because it
// is the most expensive silent bug in RADIUS-based ISP billing.
func TestTotalOctets_Gigawords(t *testing.T) {
	got := TotalOctets(100, 12)
	const want = uint64(51539607652)
	if got != want {
		t.Fatalf("TotalOctets(100, 12) = %d, want %d", got, want)
	}
}

func TestTotalOctets_Table(t *testing.T) {
	cases := []struct {
		name    string
		oct, gw uint32
		want    uint64
	}{
		{"zero", 0, 0, 0},
		{"below wrap", 1000, 0, 1000},
		{"exactly one wrap", 0, 1, 4294967296},
		{"one wrap plus one", 1, 1, 4294967297},
		{"max low word", math.MaxUint32, 0, 4294967295},
		{"11 wraps plus remainder", 1546188226, 11, 48790828482},
		{"spec example", 100, 12, 51539607652},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TotalOctets(c.oct, c.gw); got != c.want {
				t.Errorf("TotalOctets(%d,%d) = %d, want %d", c.oct, c.gw, got, c.want)
			}
		})
	}
}

// Truncating to 32 bits is the actual bug. Prove the naive version is wrong so
// nobody "simplifies" TotalOctets back into it.
func TestTotalOctets_NaiveVersionIsWrong(t *testing.T) {
	const fiftyGB = uint64(50) << 30
	oct := uint32(fiftyGB & 0xFFFFFFFF)
	gw := uint32(fiftyGB >> 32)

	if correct := TotalOctets(oct, gw); correct != fiftyGB {
		t.Fatalf("correct reassembly failed: got %d want %d", correct, fiftyGB)
	}
	naive := uint64(oct) // what you get if you ignore gigawords
	if naive == fiftyGB {
		t.Fatal("test is meaningless: 50GB does not exceed the 32-bit wrap")
	}
	if naive != fiftyGB%(1<<32) {
		t.Fatalf("naive = %d, expected the truncated value %d", naive, fiftyGB%(1<<32))
	}
	t.Logf("50 GiB billed naively as %d bytes (%.2f GiB) — the leak", naive, float64(naive)/(1<<30))
}

func TestSessionUsage_Total(t *testing.T) {
	u := SessionUsage{
		InputOctets: 100, InputGigawords: 12,
		OutputOctets: 50, OutputGigawords: 1,
	}
	want := uint64(51539607652) + uint64(4294967346)
	if got := u.Total(); got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
	}
}

// §21A.8 — replay and out-of-order packets must be no-ops.
func TestDelta_IdempotentUnderReplay(t *testing.T) {
	cases := []struct {
		name             string
		cumulative, mark uint64
		want             uint64
	}{
		{"first packet", 1000, 0, 1000},
		{"growth", 2000, 1000, 1000},
		{"exact replay", 2000, 2000, 0},
		{"out of order (older)", 1000, 2000, 0},
		{"way out of order", 1, 999999, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Delta(c.cumulative, c.mark); got != c.want {
				t.Errorf("Delta(%d,%d) = %d, want %d", c.cumulative, c.mark, got, c.want)
			}
		})
	}
}

// The invariant, exercised as a sequence: whatever order and however many
// duplicates, the total applied equals the highest cumulative value seen.
func TestDelta_SequenceInvariant(t *testing.T) {
	packets := []uint64{1000, 2000, 2000, 1500, 3000, 3000, 2500, 5000, 100}
	var applied, mark uint64
	for _, p := range packets {
		d := Delta(p, mark)
		applied += d
		if p > mark {
			mark = p
		}
	}
	const want = uint64(5000) // the maximum, not the sum
	if applied != want {
		t.Fatalf("applied %d, want %d (invariant: total == max cumulative)", applied, want)
	}
}

// §78 — concurrency. The pure function must be safe under -race.
func TestDelta_ConcurrentReadersAreSafe(t *testing.T) {
	var wg sync.WaitGroup
	results := make([]uint64, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = Delta(uint64(i)*1000, 50000)
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		want := Delta(uint64(i)*1000, 50000)
		if got != want {
			t.Fatalf("goroutine %d got %d, want %d", i, got, want)
		}
	}
}

// §21A.3 — budget computation, including the v1.2 floor.
func TestSessionBudget(t *testing.T) {
	p := DefaultBudgetPolicy()
	gib := uint64(1) << 30

	t.Run("large pool is capped at MaxPerSession", func(t *testing.T) {
		c := Counter{QuotaBytes: 100 * gib, ConsumedBytes: 0}
		b, metered := SessionBudget(c, p)
		if !metered {
			t.Fatal("expected metered")
		}
		if b != p.MaxPerSession {
			t.Fatalf("budget = %d, want cap %d", b, p.MaxPerSession)
		}
	})

	t.Run("mid pool uses the fraction", func(t *testing.T) {
		c := Counter{QuotaBytes: 4 * gib, ConsumedBytes: 0}
		b, _ := SessionBudget(c, p)
		if b != gib { // 25% of 4 GiB
			t.Fatalf("budget = %d, want %d", b, gib)
		}
	})

	t.Run("exhausted pool yields zero", func(t *testing.T) {
		c := Counter{QuotaBytes: gib, ConsumedBytes: gib}
		b, metered := SessionBudget(c, p)
		if b != 0 || !metered {
			t.Fatalf("budget = %d metered = %v, want 0 true", b, metered)
		}
	})

	t.Run("over-consumed pool yields zero, not underflow", func(t *testing.T) {
		c := Counter{QuotaBytes: gib, ConsumedBytes: gib * 3}
		if b, _ := SessionBudget(c, p); b != 0 {
			t.Fatalf("budget = %d, want 0 (underflow guard)", b)
		}
	})

	t.Run("unmetered signals no Total-Limit attribute", func(t *testing.T) {
		c := Counter{Unmetered: true}
		b, metered := SessionBudget(c, p)
		if metered {
			t.Fatal("unmetered counter must report metered=false")
		}
		if b != 0 {
			t.Fatalf("budget = %d, want 0", b)
		}
	})

	// v1.2: without the floor this asymptotes and the tail is unusable.
	t.Run("small remainder is granted whole, not fractioned", func(t *testing.T) {
		c := Counter{QuotaBytes: 100 << 20, ConsumedBytes: 0} // 100 MiB
		b, _ := SessionBudget(c, p)
		if b != 100<<20 {
			t.Fatalf("budget = %d, want the full remaining %d", b, uint64(100)<<20)
		}
	})

	t.Run("repeated small grants terminate", func(t *testing.T) {
		c := Counter{QuotaBytes: 200 << 20}
		for i := 0; i < 50; i++ {
			b, _ := SessionBudget(c, p)
			if c.Remaining() > 0 && b == 0 {
				t.Fatalf("iteration %d: budget 0 with %d bytes remaining (asymptote)", i, c.Remaining())
			}
			c.ConsumedBytes += b
			if c.Exhausted() {
				return
			}
		}
		t.Fatalf("pool never drained: consumed %d of %d", c.ConsumedBytes, c.QuotaBytes)
	})
}

// RouterOS treats Total-Limit=0 as "no traffic". A budget that is an exact
// multiple of 2^32 would emit exactly that.
func TestClampBudget_AvoidsZeroLowWord(t *testing.T) {
	cases := []uint64{1 << 32, 2 << 32, 16 << 32}
	for _, b := range cases {
		clamped := ClampBudget(b)
		low, high := SplitGigawords(clamped)
		if low == 0 {
			t.Fatalf("budget %d clamped to %d still has zero low word (high=%d)", b, clamped, high)
		}
	}
	if ClampBudget(0) != 0 {
		t.Fatal("zero budget must stay zero")
	}
	if got := ClampBudget(5000); got != 5000 {
		t.Fatalf("non-boundary budget altered: %d", got)
	}
}

func TestSplitGigawords_RoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 4294967295, 4294967296, 51539607652, 1 << 40} {
		low, high := SplitGigawords(v)
		if got := TotalOctets(low, high); got != v {
			t.Fatalf("round trip of %d gave %d", v, got)
		}
	}
}

// §21A.5 — interim interval tightens near exhaustion, never below 60s.
func TestInterimInterval(t *testing.T) {
	base, fine := 300*time.Second, 60*time.Second
	const hundredMbps = uint64(100_000_000)

	t.Run("large budget uses base", func(t *testing.T) {
		if got := InterimInterval(50<<30, hundredMbps, base, fine); got != base {
			t.Fatalf("got %v, want %v", got, base)
		}
	})
	t.Run("small budget tightens", func(t *testing.T) {
		// 100 MiB at 12.5 MB/s burns in ~8s, well under 300s.
		if got := InterimInterval(100<<20, hundredMbps, base, fine); got != fine {
			t.Fatalf("got %v, want %v", got, fine)
		}
	})
	t.Run("never below the 60s floor", func(t *testing.T) {
		if got := InterimInterval(1, hundredMbps, 1*time.Second, 1*time.Second); got < 60*time.Second {
			t.Fatalf("got %v, must be >= 60s", got)
		}
	})
	t.Run("zero line rate does not divide by zero", func(t *testing.T) {
		if got := InterimInterval(1000, 0, base, fine); got != base {
			t.Fatalf("got %v, want %v", got, base)
		}
	})
}

// §24A.3 — the reaper threshold.
func TestStaleAfter(t *testing.T) {
	if got := StaleAfter(300*time.Second, 60*time.Second); got != 960*time.Second {
		t.Fatalf("got %v, want 960s (3*300+60, per §24A.3)", got)
	}
}

// §21A.7 — period boundaries computed in tenant time, stored UTC.
func TestNextPeriod(t *testing.T) {
	lagos, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	t.Run("daily boundary uses tenant midnight", func(t *testing.T) {
		// 00:30 Lagos on 12 Aug == 23:30 UTC on 11 Aug. The period must be
		// the 12th in Lagos, not the 11th.
		at := time.Date(2026, 8, 11, 23, 30, 0, 0, time.UTC)
		s, e, err := NextPeriod(ResetDaily, at, lagos, time.Time{}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		wantStart := time.Date(2026, 8, 12, 0, 0, 0, 0, lagos).UTC()
		if !s.Equal(wantStart) {
			t.Fatalf("start = %v, want %v", s, wantStart)
		}
		if e.Sub(s) != 24*time.Hour {
			t.Fatalf("period length = %v, want 24h", e.Sub(s))
		}
		if !at.Before(e) || at.Before(s) {
			t.Fatalf("packet time %v not inside [%v, %v)", at, s, e)
		}
	})

	t.Run("monthly anchors on the subscription day", func(t *testing.T) {
		subStart := time.Date(2026, 1, 15, 0, 0, 0, 0, lagos)
		at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
		s, e, err := NextPeriod(ResetMonthly, at, lagos, subStart, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if s.In(lagos).Day() != 15 {
			t.Fatalf("start day = %d, want 15", s.In(lagos).Day())
		}
		if !at.After(s) || !at.Before(e) {
			t.Fatalf("packet time %v not inside [%v, %v)", at, s, e)
		}
	})

	// A subscription anchored on the 31st: the period containing 20 Feb runs
	// [31 Jan, 28 Feb). The end is clamped; the start is not.
	//
	// This is where Go's AddDate bites: Jan 31 + 1 month normalizes to Mar 3,
	// which would skip February entirely and hand the customer a 59-day period.
	t.Run("monthly anchored on the 31st clamps the February end", func(t *testing.T) {
		subStart := time.Date(2026, 1, 31, 0, 0, 0, 0, lagos)
		at := time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC)
		s, e, err := NextPeriod(ResetMonthly, at, lagos, subStart, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		ls, le := s.In(lagos), e.In(lagos)
		if ls.Month() != time.January || ls.Day() != 31 {
			t.Errorf("start = %v, want 31 Jan", ls)
		}
		if le.Month() != time.February || le.Day() != 28 {
			t.Errorf("end = %v, want 28 Feb (clamped, not 3 Mar)", le)
		}
		if !at.After(s) || !at.Before(e) {
			t.Errorf("packet time %v not inside [%v, %v)", at, ls, le)
		}
	})

	// Periods must tile without gaps or overlaps, or traffic at a boundary
	// lands in no period at all and quota_apply raises ErrNoCounter.
	t.Run("consecutive monthly periods tile exactly", func(t *testing.T) {
		subStart := time.Date(2026, 1, 31, 0, 0, 0, 0, lagos)
		probes := []time.Time{
			time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
		}
		var prevEnd time.Time
		for _, at := range probes {
			s, e, err := NextPeriod(ResetMonthly, at, lagos, subStart, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if !at.After(s) || !at.Before(e) {
				t.Fatalf("%v not inside [%v, %v)", at, s, e)
			}
			if !prevEnd.IsZero() && !s.Equal(prevEnd) {
				t.Fatalf("gap or overlap: previous end %v, next start %v", prevEnd, s)
			}
			prevEnd = e
		}
	})

	t.Run("unknown policy is an error, not a default", func(t *testing.T) {
		if _, _, err := NextPeriod("WEEKLY", time.Now(), lagos, time.Time{}, time.Time{}); err == nil {
			t.Fatal("expected error for unknown policy")
		}
	})
}

func TestCounter_Remaining(t *testing.T) {
	if c := (Counter{QuotaBytes: 100, ConsumedBytes: 30}); c.Remaining() != 70 {
		t.Fatalf("Remaining() = %d, want 70", c.Remaining())
	}
	// Over-consumption must floor at zero, not wrap around uint64.
	if c := (Counter{QuotaBytes: 100, ConsumedBytes: 500}); c.Remaining() != 0 {
		t.Fatalf("Remaining() = %d, want 0", c.Remaining())
	}
	if c := (Counter{Unmetered: true}); c.Remaining() != math.MaxUint64 {
		t.Fatal("unmetered counter must report unbounded remaining")
	}
}
