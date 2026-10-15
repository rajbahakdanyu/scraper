package notify

import "testing"

func TestSendEmptySummaryIsNoop(t *testing.T) {
	if err := Send(""); err != nil {
		t.Fatalf("Send(\"\") = %v, want nil", err)
	}
}
