package plan

import (
	"context"
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// BenchmarkBuild2000Instances plans a converged for_each resource with 2000
// instances, the scale of a large asset set. State lookups must stay linear-
// time-cheap per resource so the whole plan isn't quadratic in allocations.
func BenchmarkBuild2000Instances(b *testing.B) {
	p := mock.New()
	s := state.New()
	var desired []DesiredResource
	for i := 0; i < 2000; i++ {
		key := fmt.Sprintf("sprites/%04d.png", i)
		id := fmt.Sprintf("mock_thing/%d", i)
		attrs := provider.Attributes{"name": cty.StringVal(key)}
		p.Seed(id, attrs)
		s.Upsert(state.Resource{Type: "mock_thing", Name: "sprites", Key: key, ID: id})
		desired = append(desired, DesiredResource{Type: "mock_thing", Name: "sprites", Key: key, Attributes: attrs})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Build(context.Background(), p, s, desired); err != nil {
			b.Fatal(err)
		}
	}
}
