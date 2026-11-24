package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"sort"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// functions is the function library available to every config expression
// (locals, resource and import bodies, for_each, the provider block). It is the
// go-cty stdlib subset Terraform users expect, plus a few pure-Go file and path
// helpers. Relative paths resolve against the current working directory, the
// same base the file/icon_file resource attributes use; `path.config` gives the
// config file's directory for configs that should work from anywhere.
func functions() map[string]function.Function {
	return map[string]function.Function{
		"env": envFunc(),

		"toset":  stdlib.MakeToFunc(cty.Set(cty.DynamicPseudoType)),
		"tomap":  stdlib.MakeToFunc(cty.Map(cty.DynamicPseudoType)),
		"tolist": stdlib.MakeToFunc(cty.List(cty.DynamicPseudoType)),

		"merge":    stdlib.MergeFunc,
		"keys":     stdlib.KeysFunc,
		"values":   stdlib.ValuesFunc,
		"lookup":   stdlib.LookupFunc,
		"contains": stdlib.ContainsFunc,
		"length":   stdlib.LengthFunc,
		"concat":   stdlib.ConcatFunc,
		"distinct": stdlib.DistinctFunc,
		"flatten":  stdlib.FlattenFunc,

		"format":     stdlib.FormatFunc,
		"join":       stdlib.JoinFunc,
		"split":      stdlib.SplitFunc,
		"lower":      stdlib.LowerFunc,
		"upper":      stdlib.UpperFunc,
		"replace":    stdlib.ReplaceFunc,
		"trimprefix": stdlib.TrimPrefixFunc,
		"trimsuffix": stdlib.TrimSuffixFunc,
		"trimspace":  stdlib.TrimSpaceFunc,
		"regex":      stdlib.RegexFunc,

		"jsondecode": stdlib.JSONDecodeFunc,
		"jsonencode": stdlib.JSONEncodeFunc,

		"fileset":    filesetFunc(),
		"file":       fileFunc(),
		"filesha256": filesha256Func(),
		"basename":   basenameFunc(),
		"dirname":    dirnameFunc(),
	}
}

// filesetFunc implements fileset(dir, pattern): the set of FILES (never
// directories) under dir matching a doublestar pattern (`**` crosses
// directories). Results are relative to dir and always use forward slashes, so
// the keys are identical on every OS. A missing dir is an error rather than an
// empty set: it is almost always a typo, and an empty for_each would plan to
// destroy every existing instance.
func filesetFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "dir", Type: cty.String},
			{Name: "pattern", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.Set(cty.String)),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			dir, pattern := args[0].AsString(), args[1].AsString()
			if !doublestar.ValidatePattern(pattern) {
				return cty.NilVal, fmt.Errorf("invalid pattern %q", pattern)
			}
			info, err := os.Stat(dir)
			if errors.Is(err, fs.ErrNotExist) {
				return cty.NilVal, fmt.Errorf("directory %q does not exist", dir)
			}
			if err != nil {
				return cty.NilVal, err
			}
			if !info.IsDir() {
				return cty.NilVal, fmt.Errorf("%q is not a directory", dir)
			}
			matches, err := doublestar.Glob(os.DirFS(dir), pattern,
				doublestar.WithFilesOnly(), doublestar.WithFailOnIOErrors())
			if err != nil {
				return cty.NilVal, err
			}
			if len(matches) == 0 {
				return cty.SetValEmpty(cty.String), nil
			}
			sort.Strings(matches)
			vals := make([]cty.Value, len(matches))
			for i, m := range matches {
				vals[i] = cty.StringVal(m)
			}
			return cty.SetVal(vals), nil
		},
	})
}

// filesha256Func implements filesha256(path): the hex SHA-256 of the file's
// contents, as in Terraform.
func filesha256Func() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			b, err := os.ReadFile(args[0].AsString())
			if err != nil {
				return cty.NilVal, err
			}
			sum := sha256.Sum256(b)
			return cty.StringVal(hex.EncodeToString(sum[:])), nil
		},
	})
}

// fileFunc implements file(path): the file's contents as a UTF-8 string.
func fileFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			p := args[0].AsString()
			b, err := os.ReadFile(p)
			if err != nil {
				return cty.NilVal, err
			}
			if !utf8.Valid(b) {
				return cty.NilVal, fmt.Errorf("%s is not valid UTF-8 text", p)
			}
			return cty.StringVal(string(b)), nil
		},
	})
}

// basenameFunc and dirnameFunc use forward-slash semantics on every OS, to
// match the keys fileset returns.
func basenameFunc() function.Function { return pathFunc(pathpkg.Base) }
func dirnameFunc() function.Function  { return pathFunc(pathpkg.Dir) }

func pathFunc(fn func(string) string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.StringVal(fn(args[0].AsString())), nil
		},
	})
}

func envFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "name", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.StringVal(os.Getenv(args[0].AsString())), nil
		},
	})
}
