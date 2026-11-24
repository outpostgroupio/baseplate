package resources

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// filteredText implements provider.Censoring for resources whose names and
// descriptions go through Roblox's text filter. Embed it in the resource.
// Roblox replaces each character of a word the filter rejects with '#', so
// the config's text keeps reading back differently and would otherwise plan
// an update on every run.
type filteredText struct{}

var _ provider.Censoring = filteredText{}

// Censored reports whether actual is desired with some characters replaced by
// '#': the same number of characters, each one either equal or a '#'.
func (filteredText) Censored(_ string, desired, actual cty.Value) bool {
	if !desired.Type().Equals(cty.String) || !actual.Type().Equals(cty.String) ||
		desired.IsNull() || actual.IsNull() || !desired.IsKnown() || !actual.IsKnown() {
		return false
	}
	d, a := []rune(desired.AsString()), []rune(actual.AsString())
	if len(d) != len(a) {
		return false
	}
	masked := false
	for i := range d {
		switch {
		case a[i] == d[i]:
		case a[i] == '#':
			masked = true
		default:
			return false
		}
	}
	return masked
}
