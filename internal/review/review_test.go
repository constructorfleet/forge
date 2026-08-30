package review_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/review"
)

func TestMapAxisSeverity_MapsHighMedLowOntoErrorWarningInfo(t *testing.T) {
	cases := []struct {
		name string
		in   review.AxisSeverity
		want review.Severity
	}{
		{"high maps to error", review.AxisSeverityHigh, review.SeverityError},
		{"med maps to warning", review.AxisSeverityMed, review.SeverityWarning},
		{"low maps to info", review.AxisSeverityLow, review.SeverityInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := review.MapAxisSeverity(c.in)
			if got != c.want {
				t.Errorf("MapAxisSeverity(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMapAxisSeverity_UnrecognizedAxisSeverityDefaultsToInfo(t *testing.T) {
	got := review.MapAxisSeverity(review.AxisSeverity("bogus"))
	if got != review.SeverityInfo {
		t.Errorf("MapAxisSeverity(bogus) = %q, want default %q", got, review.SeverityInfo)
	}
}
