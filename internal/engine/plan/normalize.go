package plan

import (
	"fmt"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// normalize applies a resource's optional provider.Normalizer to its desired
// attributes, so the diff compares canonical forms (e.g. RFC 3339 timestamps
// in UTC). Resources without the capability pass through unchanged. An error
// is prefixed with the resource address so it reads well at plan time.
func normalize(res provider.Resource, address string, desired provider.Attributes) (provider.Attributes, error) {
	n, ok := res.(provider.Normalizer)
	if !ok {
		return desired, nil
	}
	out, err := n.Normalize(desired)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", address, err)
	}
	return out, nil
}
