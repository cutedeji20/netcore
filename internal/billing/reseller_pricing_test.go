package billing

import "testing"

func TestCalculateResellerPrice(t *testing.T) {
	for _, tc := range []struct {
		base          int64
		bps           int
		discount, net int64
	}{
		{10001, 0, 0, 10001},
		{10001, 2500, 2500, 7501},
		{10001, 9999, 9999, 2},
		{9223372036854775807, 5000, 4611686018427387903, 4611686018427387904},
	} {
		discount, net, err := CalculateResellerPrice(tc.base, tc.bps)
		if err != nil || discount != tc.discount || net != tc.net {
			t.Fatalf("base=%d bps=%d: discount=%d net=%d err=%v", tc.base, tc.bps, discount, net, err)
		}
	}
}

func TestCalculateResellerPriceRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		base int64
		bps  int
	}{{-1, 100}, {1, -1}, {1, 10000}} {
		if _, _, err := CalculateResellerPrice(tc.base, tc.bps); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
