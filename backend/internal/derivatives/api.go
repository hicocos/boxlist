package derivatives

import (
	"encoding/json"
	"strings"
)

// SQL keeps canonical comma/newline-delimited values; the HTTP contract exposes
// arrays. These methods deliberately avoid serializing storage bindings/keys.
func (j DerivativeJob) MarshalJSON() ([]byte, error) {
	type alias DerivativeJob
	return json.Marshal(struct {
		alias
		Profiles []string `json:"profiles"`
	}{alias(j), profileList(j.Profiles)})
}
func (i DerivativeItem) MarshalJSON() ([]byte, error) {
	type alias DerivativeItem
	return json.Marshal(struct {
		alias
		Profiles []string `json:"profiles"`
	}{alias(i), profileList(i.Profiles)})
}
func (r DerivativeRule) MarshalJSON() ([]byte, error) {
	type alias DerivativeRule
	excludes := []string{}
	if r.Excludes != "" {
		excludes = strings.Split(r.Excludes, "\n")
	}
	return json.Marshal(struct {
		alias
		Profiles      []string `json:"profiles"`
		Excludes      []string `json:"excludes"`
		IntervalHours float64  `json:"interval_hours"`
		LastScanAt    any      `json:"last_scan_at"`
		NextScanAt    any      `json:"next_scan_at"`
		LastError     string   `json:"last_error,omitempty"`
	}{alias(r), profileList(r.Profiles), excludes, float64(r.IntervalMinutes) / 60, r.LastRun, r.NextRun, r.PauseReason})
}
func profileList(p string) []string {
	if p == "" {
		return []string{}
	}
	return strings.Split(p, ",")
}
