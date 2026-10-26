package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// hashFile is the hex SHA-256 of a file's content, for a content_hash trigger.
// A missing file is a provider.TriggerUnavailableError, so a plan for a
// resource already in state warns (keeping the recorded hash) instead of
// failing, e.g. in CI without the source files. Other read errors are errors.
func hashFile(resourceType, attr, path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", &provider.TriggerUnavailableError{Reason: fmt.Sprintf("%s %q not found", attr, path)}
	}
	if err != nil {
		return "", fmt.Errorf("%s: reading %s %q: %w", resourceType, attr, path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// contentChanged reports whether sum differs from the content_hash recorded
// before an update (prior). No recorded hash counts as changed.
func contentChanged(prior map[string]cty.Value, sum string) bool {
	was, ok := prior["content_hash"]
	if !ok || was.IsNull() || !was.IsKnown() || was.Type() != cty.String {
		return true
	}
	return was.AsString() != sum
}
