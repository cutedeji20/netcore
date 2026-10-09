package payments

import (
	"math"

	"github.com/netcore-isp/netcore/internal/billing"
)

type directPlanSnapshot struct {
	BasePlanMinor        int64
	DiscountBasisPoints  int
	DiscountMinor        int64
	NetPlanMinor         int64
	BankChargeMinor      int64
	TotalMinor           int64
	ResellerAtInitiation bool
}

func calculateDirectPlanSnapshot(baseMinor, bankChargeMinor int64, discountBasisPoints int, reseller bool) (directPlanSnapshot, error) {
	if baseMinor <= 0 || bankChargeMinor < 0 {
		return directPlanSnapshot{}, ErrInvalidRequest
	}
	if !reseller {
		discountBasisPoints = 0
	}
	discount, net, err := billing.CalculateResellerPrice(baseMinor, discountBasisPoints)
	if err != nil || net > math.MaxInt64-bankChargeMinor {
		return directPlanSnapshot{}, ErrInvalidRequest
	}
	return directPlanSnapshot{BasePlanMinor: baseMinor, DiscountBasisPoints: discountBasisPoints, DiscountMinor: discount, NetPlanMinor: net, BankChargeMinor: bankChargeMinor, TotalMinor: net + bankChargeMinor, ResellerAtInitiation: reseller}, nil
}
