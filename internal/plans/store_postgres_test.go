package plans

import "testing"

func TestSubscribedPlanAllowsPriceChangeOnly(t *testing.T) {
	price := int64(50000)
	plan := Plan{
		Name: "Day Pass", Description: "One day", PriceMinor: price, Currency: "NGN",
		DurationSeconds: 86400, DownloadBPS: 20_000_000, UploadBPS: 10_000_000,
		MaxDevices: 2, MaxConcurrentSessions: 1, QuotaResetPolicy: "NONE",
	}
	input := WriteInput{
		Name: plan.Name, Description: plan.Description, PriceMinor: 75000, Currency: plan.Currency,
		DurationSeconds: plan.DurationSeconds, DownloadBPS: plan.DownloadBPS, UploadBPS: plan.UploadBPS,
		MaxDevices: plan.MaxDevices, MaxConcurrentSessions: plan.MaxConcurrentSessions,
		QuotaResetPolicy: plan.QuotaResetPolicy,
	}
	if !sameNonPriceTerms(plan, input) {
		t.Fatal("a price-only change should preserve locked subscription terms")
	}
	input.DurationSeconds++
	if sameNonPriceTerms(plan, input) {
		t.Fatal("changing subscription duration must remain locked")
	}
}
