package vouchers

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPrintableBatchPDFUsesTwelveTicketsPerA4Page(t *testing.T) {
	quota := int64(5 * 1024 * 1024 * 1024)
	expires := time.Date(2027, 1, 31, 12, 0, 0, 0, time.UTC)
	batch := PrintableBatch{ID: "33333333-3333-4333-8333-333333333333", PlanName: "Phone Weekly", DurationSeconds: 7 * 24 * 60 * 60, QuotaBytes: &quota, MaxDevices: 2, ExpiresAt: &expires}
	for index := 1; index <= 13; index++ {
		batch.Codes = append(batch.Codes, IssuedCode{Code: fmt.Sprintf("NC-2345-6789-%04d", index)})
	}
	document, err := RenderPrintableBatchPDF(batch)
	if err != nil {
		t.Fatal(err)
	}
	text := string(document)
	if !strings.HasPrefix(text, "%PDF-1.4") || !strings.Contains(text, "/Count 2") {
		t.Fatalf("expected a two-page PDF, got %d bytes", len(document))
	}
	for index := 1; index <= 13; index++ {
		if !strings.Contains(text, fmt.Sprintf("NC-2345-6789-%04d", index)) {
			t.Fatalf("ticket %d was omitted", index)
		}
	}
	if strings.Count(text, "PREPAID INTERNET ACCESS") != 13 {
		t.Fatalf("expected 13 ticket layouts")
	}
	if strings.Contains(text, "Connect to Data Hub Wi-Fi") || strings.Contains(text, "enter this code on the voucher form") {
		t.Fatal("obsolete connection instructions were included in the printable ticket")
	}
}

func TestPrintableBatchPDFRejectsEmptyBatch(t *testing.T) {
	if _, err := RenderPrintableBatchPDF(PrintableBatch{ID: "batch"}); err == nil {
		t.Fatal("empty batch was printable")
	}
}
