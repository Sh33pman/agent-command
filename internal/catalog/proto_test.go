package catalog

import (
	"errors"
	"strings"
	"testing"
)

func TestNegotiatePicksTheHighestShared(t *testing.T) {
	cases := []struct {
		mine, theirs Range
		want         int
	}{
		{Range{1, 1}, Range{1, 1}, 1},
		{Range{1, 3}, Range{2, 5}, 3},
		{Range{2, 5}, Range{1, 3}, 3},
		{Range{1, 4}, Range{2, 2}, 2},
		{Range{3, 3}, Range{1, 3}, 3},
	}
	for _, tc := range cases {
		got, err := Negotiate(tc.mine, tc.theirs)
		if err != nil || got != tc.want {
			t.Errorf("Negotiate(%v, %v) = %d, %v; want %d", tc.mine, tc.theirs, got, err, tc.want)
		}
	}
}

func TestNegotiateSaysWhichSideToUpdate(t *testing.T) {
	cases := []struct {
		name         string
		mine, theirs Range
		update, says string
	}{
		{"they're older", Range{2, 3}, Range{1, 1}, "theirs", "update the other side"},
		{"I'm older", Range{1, 1}, Range{2, 3}, "mine", "update this side"},
		{"mine isn't a range", Range{2, 1}, Range{1, 1}, "", "invalid protocol range"},
		{"theirs isn't a range", Range{1, 1}, Range{0, 1}, "", "invalid protocol range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Negotiate(tc.mine, tc.theirs)
			var pe *ProtoError
			if !errors.As(err, &pe) {
				t.Fatalf("Negotiate = %d, %v; want a *ProtoError", v, err)
			}
			if pe.Code() != CodeProtoUnsupported {
				t.Errorf("code %q, want %q", pe.Code(), CodeProtoUnsupported)
			}
			if pe.Update() != tc.update {
				t.Errorf("Update() = %q, want %q", pe.Update(), tc.update)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error %q doesn't say %q", err, tc.says)
			}
		})
	}
}

func TestRangeString(t *testing.T) {
	if got := (Range{1, 1}).String(); got != "1" {
		t.Errorf("got %q, want 1", got)
	}
	if got := (Range{1, 3}).String(); got != "1–3" {
		t.Errorf("got %q, want 1–3", got)
	}
}
