package billing

import "errors"

var ErrInvalidResellerDiscount = errors.New("billing: invalid reseller discount")

// CalculateResellerPrice floors the discount in minor currency units without
// multiplying the full price by basis points, which could overflow int64.
func CalculateResellerPrice(baseMinor int64, discountBasisPoints int) (discountMinor, netMinor int64, err error) {
	if baseMinor < 0 || discountBasisPoints < 0 || discountBasisPoints > 9999 {
		return 0, 0, ErrInvalidResellerDiscount
	}
	bps := int64(discountBasisPoints)
	discountMinor = (baseMinor/10000)*bps + ((baseMinor%10000)*bps)/10000
	return discountMinor, baseMinor - discountMinor, nil
}
