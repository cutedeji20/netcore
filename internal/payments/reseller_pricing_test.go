package payments

import "testing"

func TestDirectPlanSnapshotDiscountBeforeBankCharge(t *testing.T) {
	price, err := calculateDirectPlanSnapshot(10001, 1500, 2500, true)
	if err != nil {
		t.Fatal(err)
	}
	if price.BasePlanMinor != 10001 || price.DiscountMinor != 2500 || price.NetPlanMinor != 7501 || price.BankChargeMinor != 1500 || price.TotalMinor != 9001 || !price.ResellerAtInitiation {
		t.Fatalf("price=%+v", price)
	}
	regular, err := calculateDirectPlanSnapshot(10001, 1500, 2500, false)
	if err != nil || regular.TotalMinor != 11501 || regular.DiscountMinor != 0 {
		t.Fatalf("regular=%+v err=%v", regular, err)
	}
}
