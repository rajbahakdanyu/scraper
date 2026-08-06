package fileutil

import "testing"

func TestHumanBytes(t *testing.T) {
	for _, test := range []struct {
		n    int64
		want string
	}{
		{n: -1, want: "?"},
		{n: 0, want: "0B"},
		{n: 1024, want: "1.0K"},
	} {
		if got := HumanBytes(test.n); got != test.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", test.n, got, test.want)
		}
	}
}
