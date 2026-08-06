package disk

import "fmt"

func FormatBytes(n int64) string {
	if n == 0 {
		return "0B"
	}
	units := []string{"B", "K", "M", "G", "T"}
	unitIdx := 0
	f := float64(n)
	for f >= 1024 && unitIdx < len(units)-1 {
		f /= 1024
		unitIdx++
	}
	if unitIdx == 0 {
		return fmt.Sprintf("%.0fB", f)
	}
	return fmt.Sprintf("%.1f%s", f, units[unitIdx])
}
