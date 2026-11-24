package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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

// iconTriggers is the content_hash trigger for an optional icon_file. Without
// icon_file it records an empty hash rather than none, so adding icon_file to
// an object already in state diffs (and uploads) instead of being adopted as
// the baseline.
func iconTriggers(resourceType string, desired provider.Attributes) (map[string]cty.Value, error) {
	path := stringOr(desired, "icon_file")
	if path == "" {
		return map[string]cty.Value{"content_hash": cty.StringVal("")}, nil
	}
	sum, err := hashFile(resourceType, "icon_file", path)
	if err != nil {
		return nil, err
	}
	return map[string]cty.Value{"content_hash": cty.StringVal(sum)}, nil
}

// changedIcon returns the icon_file to upload with an update: its content,
// name and type when its content_hash differs from the one recorded before
// this update (prior), and nothing otherwise, so a metadata-only edit doesn't
// re-upload (and re-moderate) the icon. A missing icon file returns nothing:
// its content can't have been checked.
func changedIcon(resourceType string, desired provider.Attributes, prior map[string]cty.Value) (data []byte, name, typ string, err error) {
	path := stringOr(desired, "icon_file")
	if path == "" {
		return nil, "", "", nil
	}
	sum, err := hashFile(resourceType, "icon_file", path)
	var unavailable *provider.TriggerUnavailableError
	if errors.As(err, &unavailable) {
		return nil, "", "", nil
	}
	if err != nil || !contentChanged(prior, sum) {
		return nil, "", "", err
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, "", "", fmt.Errorf("%s: reading icon_file %q: %w", resourceType, path, err)
	}
	return data, filepath.Base(path), contentType(path), nil
}
