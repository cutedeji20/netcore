package vouchers

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const ticketsPerPage = 20

// RenderPrintableBatchPDF creates an A4 sheet with four columns and five rows.
// It intentionally uses only the standard PDF fonts so production rendering
// does not depend on a browser, system font, or external service.
func RenderPrintableBatchPDF(batch PrintableBatch) ([]byte, error) {
	if batch.ID == "" || len(batch.Codes) == 0 {
		return nil, ErrCodeUnavailable
	}
	pageCount := (len(batch.Codes) + ticketsPerPage - 1) / ticketsPerPage
	objects := make([][]byte, 5+pageCount*2)
	objects[1] = []byte("<< /Type /Catalog /Pages 2 0 R >>")
	objects[3] = []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	objects[4] = []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>")

	pageRefs := make([]string, 0, pageCount)
	for page := 0; page < pageCount; page++ {
		pageObject := 5 + page*2
		contentObject := pageObject + 1
		pageRefs = append(pageRefs, fmt.Sprintf("%d 0 R", pageObject))
		content := printablePageContent(batch, page)
		objects[contentObject] = []byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		objects[pageObject] = []byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595.28 841.89] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>", contentObject))
	}
	objects[2] = []byte(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", pageCount, strings.Join(pageRefs, " ")))

	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objects))
	for id := 1; id < len(objects); id++ {
		offsets[id] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n", id)
		output.Write(objects[id])
		output.WriteString("\nendobj\n")
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects))
	for id := 1; id < len(objects); id++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects), xref)
	return output.Bytes(), nil
}

func printablePageContent(batch PrintableBatch, page int) string {
	const (
		margin = 22.0
		hGap   = 6.0
		vGap   = 18.0
		width  = (595.28 - margin*2 - hGap*3) / 4
		height = 139.0
	)
	var content strings.Builder
	content.WriteString("0.45 w [3 2] 0 d 0.70 G\n")
	start := page * ticketsPerPage
	end := start + ticketsPerPage
	if end > len(batch.Codes) {
		end = len(batch.Codes)
	}
	for index := start; index < end; index++ {
		slot := index - start
		column, row := slot%4, slot/4
		x := margin + float64(column)*(width+hGap)
		y := 841.89 - margin - float64(row+1)*height - float64(row)*vGap
		fmt.Fprintf(&content, "%.2f %.2f %.2f %.2f re S\n", x, y, width, height)
		writePDFText(&content, "F2", 8.5, x+7, y+height-17, "DATA HUB")
		writePDFText(&content, "F1", 5.5, x+7, y+height-28, "PREPAID INTERNET ACCESS")
		content.WriteString("[] 0 d 0.86 G\n")
		fmt.Fprintf(&content, "%.2f %.2f m %.2f %.2f l S\n", x+7, y+height-34, x+width-7, y+height-34)
		writePDFText(&content, "F1", 5.5, x+7, y+height-49, "VOUCHER CODE")
		// Tickets use the compact presentation so customers can type the code
		// easily. Redemption normalizes both this form and legacy dashed codes.
		writePDFText(&content, "F2", 7.5, x+7, y+height-65, normalizeCode(batch.Codes[index].Code))
		writePDFText(&content, "F2", 7, x+7, y+height-83, truncatePDFText(batch.PlanName, 20))
		writePDFText(&content, "F1", 5.75, x+7, y+height-97, "Duration: "+formatVoucherDuration(batch.DurationSeconds))
		writePDFText(&content, "F1", 5.75, x+7, y+height-109, "Quota: "+formatVoucherQuota(batch.QuotaBytes))
		writePDFText(&content, "F1", 5.75, x+7, y+height-121, "Devices: "+strconv.Itoa(batch.MaxDevices))
		if batch.ExpiresAt != nil {
			writePDFText(&content, "F1", 5.25, x+7, y+height-133, "Use before: "+batch.ExpiresAt.UTC().Format("02 Jan 2006 15:04 UTC"))
		}
		content.WriteString("[3 2] 0 d 0.70 G\n")
	}
	return content.String()
}

func writePDFText(content *strings.Builder, font string, size, x, y float64, value string) {
	fmt.Fprintf(content, "BT /%s %.2f Tf %.2f %.2f Td (%s) Tj ET\n", font, size, x, y, escapePDFText(value))
}

func escapePDFText(value string) string {
	var result strings.Builder
	for _, character := range value {
		if character < 32 || character > 126 {
			result.WriteByte('?')
			continue
		}
		if character == '(' || character == ')' || character == '\\' {
			result.WriteByte('\\')
		}
		result.WriteRune(character)
	}
	return result.String()
}

func truncatePDFText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	if limit < 4 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func formatVoucherDuration(seconds int64) string {
	duration := time.Duration(seconds) * time.Second
	if duration%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d day(s)", duration/(24*time.Hour))
	}
	if duration%time.Hour == 0 {
		return fmt.Sprintf("%d hour(s)", duration/time.Hour)
	}
	return fmt.Sprintf("%d minute(s)", duration/time.Minute)
}

func formatVoucherQuota(bytes *int64) string {
	if bytes == nil {
		return "Unlimited"
	}
	const gib = int64(1024 * 1024 * 1024)
	const mib = int64(1024 * 1024)
	if *bytes%gib == 0 {
		return fmt.Sprintf("%d GiB", *bytes/gib)
	}
	return fmt.Sprintf("%d MiB", *bytes/mib)
}
