package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/config/configgen"
	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
	"github.com/outpostgroupio/baseplate/internal/version"
)

// newProvider selects the real Roblox provider when ROBLOX_API_KEY is set,
// otherwise the in-memory mock (so tests and demos work without credentials).
func newProvider() provider.Provider {
	if key := os.Getenv("ROBLOX_API_KEY"); key != "" {
		if p, err := roblox.New(key); err == nil {
			return p
		}
	}
	return mock.New()
}

// openBackend selects the state backend from the config's `backend` block,
// falling back to the local file backend when there is none. The bool is true
// when the local fallback was used (so destroy can warn on a remote-shaped setup).
func openBackend(cfgPath, statePath string) (backend.Backend, bool, error) {
	bc, err := config.LoadBackend(cfgPath)
	if err != nil {
		return nil, false, err
	}
	if bc == nil {
		return backend.NewLocal(statePath), true, nil
	}
	switch bc.Type {
	case "http":
		hc, err := httpConfigFromAttrs(bc.Attributes)
		if err != nil {
			return nil, false, err
		}
		hc.Version = version.Version
		h, err := backend.NewHTTP(hc)
		if err != nil {
			return nil, false, err
		}
		return h, false, nil
	default:
		return nil, false, fmt.Errorf("unsupported backend type %q", bc.Type)
	}
}

var httpBackendAttrs = map[string]bool{
	"address": true, "update_method": true, "lock_address": true, "unlock_address": true,
	"lock_method": true, "unlock_method": true, "username": true, "password": true,
}

func httpConfigFromAttrs(attrs map[string]cty.Value) (backend.HTTPConfig, error) {
	for k := range attrs {
		if !httpBackendAttrs[k] {
			return backend.HTTPConfig{}, fmt.Errorf("http backend: unknown attribute %q", k)
		}
	}
	str := func(k string) (string, error) {
		v, ok := attrs[k]
		if !ok {
			return "", nil
		}
		if v.IsNull() || v.Type() != cty.String {
			return "", fmt.Errorf("http backend: attribute %q must be a string", k)
		}
		return v.AsString(), nil
	}
	var c backend.HTTPConfig
	var err error
	if c.Address, err = str("address"); err != nil {
		return c, err
	}
	if c.Address == "" {
		return c, fmt.Errorf("http backend: address is required")
	}
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"update_method", &c.UpdateMethod}, {"lock_address", &c.LockAddress},
		{"unlock_address", &c.UnlockAddress}, {"lock_method", &c.LockMethod},
		{"unlock_method", &c.UnlockMethod}, {"username", &c.Username}, {"password", &c.Password},
	} {
		if *f.dst, err = str(f.key); err != nil {
			return c, err
		}
	}
	return c, nil
}

type validateResult struct {
	Valid bool `json:"valid"`
}

type planResult struct {
	Changes  []plan.JSONChange `json:"changes"`
	Summary  plan.Summary      `json:"summary"`
	Warnings []string          `json:"warnings,omitempty"`
}

type importResult struct {
	Address string `json:"address"`
	ID      string `json:"id"`
}

type applyResult struct {
	Applied int               `json:"applied"`
	Changes []plan.JSONChange `json:"changes"`
}

var errCancelled = errors.New("cancelled at confirmation prompt")

var confirmPrompts = map[string]string{
	"apply":   "Do you want to perform these actions?",
	"destroy": "Do you really want to destroy all resources?",
}

var cancelLabel = map[string]string{
	"apply":   "Apply",
	"destroy": "Destroy",
}

// confirm decides whether an apply/destroy may proceed. It auto-approves when
// --auto-approve is set or the session is non-interactive (CI). On an
// interactive TTY it prompts and accepts only "yes". Combining --json with an
// interactive TTY (no auto-approve) is rejected, since a text prompt would
// corrupt the JSON stream.
func confirm(s streams, format output.Format, autoApprove bool, verb string) (bool, error) {
	if autoApprove || !s.isTTY() {
		return true, nil
	}
	if format == output.JSON {
		return false, fmt.Errorf("%s with --json requires --auto-approve", verb)
	}
	fmt.Fprintf(s.out, "\n%s Only 'yes' will be accepted to confirm.\nEnter a value: ", confirmPrompts[verb])
	line, _ := bufio.NewReader(s.in).ReadString('\n')
	return strings.TrimSpace(line) == "yes", nil
}

// autoApproved reports whether --auto-approve or its --yes alias was set.
func autoApproved(cmd *cobra.Command) bool {
	a, _ := cmd.Flags().GetBool("auto-approve")
	y, _ := cmd.Flags().GetBool("yes")
	return a || y
}

type streams struct {
	in    io.Reader
	out   io.Writer
	err   io.Writer
	isTTY func() bool
}

func fmtOf(jsonOut bool) output.Format {
	if jsonOut {
		return output.JSON
	}
	return output.Text
}

func Execute() {
	stat, _ := os.Stdin.Stat()
	isTTY := func() bool { return stat != nil && stat.Mode()&os.ModeCharDevice != 0 }
	os.Exit(run(os.Args[1:], streams{in: os.Stdin, out: os.Stdout, err: os.Stderr, isTTY: isTTY}))
}

func run(args []string, s streams) int {
	var cfgPath, statePath, target string
	var jsonOut bool
	var generateConfigOut string
	var cmdName string

	root := &cobra.Command{Use: "baseplate", SilenceUsage: true, SilenceErrors: true}
	root.Version = version.Version
	root.PersistentFlags().StringVar(&cfgPath, "config", "baseplate.bp.hcl", "config file")
	root.PersistentFlags().StringVar(&statePath, "state", "baseplate.bpstate", "state file")
	root.PersistentFlags().StringVar(&target, "target", "", `limit to one resource (type.name, every instance) or one instance (type.name["key"])`)
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	root.SetOut(s.out)
	root.SetErr(s.err)
	root.SetArgs(args)

	exit := 0
	p := newProvider()

	load := func() (*config.Config, error) {
		return config.LoadConfig(cfgPath, p)
	}

	root.AddCommand(&cobra.Command{Use: "validate", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		if _, err := load(); err != nil {
			return err
		}
		return output.Emit(s.out, fmtOf(jsonOut), "validate", "Configuration is valid.\n", validateResult{Valid: true})
	}})

	planCmd := &cobra.Command{Use: "plan", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		tgt, err := parseTarget(target)
		if err != nil {
			return err
		}
		cfg, err := load()
		if err != nil {
			return err
		}
		desired, imports := cfg.Resources, cfg.Imports
		b, _, err := openBackend(cfgPath, statePath)
		if err != nil {
			return err
		}
		st, err := b.Get()
		if err != nil {
			return err
		}
		declared := map[string]bool{}
		for _, d := range desired {
			declared[d.Address()] = true
		}
		toResolve := imports
		if generateConfigOut != "" {
			toResolve = declaredImports(imports, declared)
		}
		pl, err := plan.Build(context.Background(), p, st, desired)
		if err != nil {
			return err
		}
		pl.EmptyForEach = cfg.EmptyForEach
		if err := plan.ResolveImports(context.Background(), p, st, pl, toResolve); err != nil {
			return err
		}
		if generateConfigOut != "" {
			if err := generateConfig(context.Background(), p, imports, declared, generateConfigOut, s.out); err != nil {
				return err
			}
		}
		filterPlanByTarget(pl, tgt)
		if err := output.Emit(s.out, fmtOf(jsonOut), "plan",
			pl.RenderText(true),
			planResult{Changes: pl.JSONChanges(), Summary: pl.Summary(), Warnings: pl.Warnings()}); err != nil {
			return err
		}
		if pl.HasChanges() {
			exit = 2
		}
		return nil
	}}
	planCmd.Flags().StringVar(&generateConfigOut, "generate-config", "", "write generated HCL for undeclared import blocks to this file")
	root.AddCommand(planCmd)

	applyCmd := &cobra.Command{Use: "apply", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		return applyRun(p, cfgPath, statePath, target, false, autoApproved(cmd), fmtOf(jsonOut), s)
	}}
	applyCmd.Flags().Bool("auto-approve", false, "skip the interactive approval prompt")
	applyCmd.Flags().Bool("yes", false, "alias for --auto-approve")
	root.AddCommand(applyCmd)

	destroyCmd := &cobra.Command{Use: "destroy", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		return applyRun(p, cfgPath, statePath, target, true, autoApproved(cmd), fmtOf(jsonOut), s)
	}}
	destroyCmd.Flags().Bool("auto-approve", false, "skip the interactive approval prompt")
	destroyCmd.Flags().Bool("yes", false, "alias for --auto-approve")
	root.AddCommand(destroyCmd)
	root.AddCommand(&cobra.Command{
		Use:   "import <address> <id>",
		Short: "Adopt an existing resource into state",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmdName = cmd.Name()
			return importRun(p, cfgPath, statePath, args[0], args[1], fmtOf(jsonOut), s)
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "show [address]",
		Short: "Print the resources recorded in state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmdName = cmd.Name()
			address := ""
			if len(args) == 1 {
				address = args[0]
			}
			return showRun(p, cfgPath, statePath, address, fmtOf(jsonOut), s)
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the baseplate version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return nil
		},
	})

	if err := root.Execute(); err != nil {
		if errors.Is(err, errCancelled) {
			return 1
		}
		output.EmitError(s.err, fmtOf(jsonOut), cmdName, err)
		return 1
	}
	return exit
}

func applyRun(p provider.Provider, cfgPath, statePath, target string, destroy, autoApprove bool, format output.Format, s streams) error {
	verb := "apply"
	if destroy {
		verb = "destroy"
	}
	tgt, err := parseTarget(target)
	if err != nil {
		return err
	}
	var desired []plan.DesiredResource
	var imports []plan.ImportBlock
	var emptyForEach []string
	if !destroy {
		cfg, err := config.LoadConfig(cfgPath, p)
		if err != nil {
			return err
		}
		desired, imports, emptyForEach = cfg.Resources, cfg.Imports, cfg.EmptyForEach
	}
	b, localFallback, err := openBackend(cfgPath, statePath)
	if err != nil {
		return err
	}
	// Warn only when there is NO config file: a remote-state user who forgot
	// --config (or mistyped it) would otherwise silently destroy local/empty
	// state and be told "0 change(s)". A config that exists but omits a backend
	// block is a deliberate local setup, so it stays quiet.
	if destroy && localFallback {
		if _, statErr := os.Stat(cfgPath); os.IsNotExist(statErr) {
			fmt.Fprintf(s.err, "note: no config at %s; using local state backend at %s\n", cfgPath, statePath)
		}
	}
	unlock, err := b.Lock()
	if err != nil {
		return err
	}
	defer func() {
		if uerr := unlock(); uerr != nil {
			fmt.Fprintf(s.err, "warning: failed to release state lock: %v\n", uerr)
		}
	}()
	st, err := b.Get()
	if err != nil {
		return err
	}
	pl, err := plan.Build(context.Background(), p, st, desired)
	if err != nil {
		return err
	}
	pl.EmptyForEach = emptyForEach
	if err := plan.ResolveImports(context.Background(), p, st, pl, imports); err != nil {
		return err
	}
	filterPlanByTarget(pl, tgt)

	if pl.HasChanges() {
		if format == output.Text {
			fmt.Fprint(s.out, pl.RenderText(true))
		}
		ok, err := confirm(s, format, autoApprove, verb)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(s.out, "%s cancelled.\n", cancelLabel[verb])
			return errCancelled
		}
	}
	if err := apply.Apply(context.Background(), p, b, st, pl); err != nil {
		return err
	}
	return output.Emit(s.out, format, verb,
		fmt.Sprintf("Apply complete. %d change(s).\n", len(pl.Changes)),
		applyResult{Applied: len(pl.Changes), Changes: pl.JSONChanges()})
}

// importRun adopts an existing resource (declared in config) into state by id,
// under the state lock. It never mutates the remote.
func importRun(p provider.Provider, cfgPath, statePath, address, id string, format output.Format, s streams) error {
	typ, name, key, _, err := addr.Parse(address)
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	address = addr.Format(typ, name, key)
	_, desired, _, err := config.Load(cfgPath, p)
	if err != nil {
		return err
	}
	b, _, err := openBackend(cfgPath, statePath)
	if err != nil {
		return err
	}
	unlock, err := b.Lock()
	if err != nil {
		return err
	}
	defer func() {
		if uerr := unlock(); uerr != nil {
			fmt.Fprintf(s.err, "warning: failed to release state lock: %v\n", uerr)
		}
	}()
	st, err := b.Get()
	if err != nil {
		return err
	}
	if err := apply.Import(context.Background(), p, b, st, address, id, desired); err != nil {
		return err
	}
	return output.Emit(s.out, format, "import",
		fmt.Sprintf("Imported %s (id %s).\n", address, id),
		importResult{Address: address, ID: id})
}

// targetAddr is a parsed --target: a whole resource (every instance, when key is
// empty and hasKey is false) or one for_each instance.
type targetAddr struct {
	typ, name, key string
	hasKey         bool
}

// matches reports whether a change (or state resource) at type/name/key is
// selected by the target.
func (t *targetAddr) matches(typ, name, key string) bool {
	if typ != t.typ || name != t.name {
		return false
	}
	return !t.hasKey || key == t.key
}

// parseTarget parses --target. An empty flag means no target (nil). A
// malformed address is an error rather than silently matching nothing.
func parseTarget(s string) (*targetAddr, error) {
	if s == "" {
		return nil, nil
	}
	typ, name, key, hasKey, err := addr.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("--target: %w", err)
	}
	return &targetAddr{typ: typ, name: name, key: key, hasKey: hasKey}, nil
}

// filterPlanByTarget restricts the plan to the targeted resource (all of its
// instances) or instance when --target is set, so targeted operations never
// touch (or delete) other resources.
func filterPlanByTarget(pl *plan.Plan, t *targetAddr) {
	if t == nil {
		return
	}
	var out []plan.Change
	for _, c := range pl.Changes {
		if t.matches(c.Type, c.Name, c.Key) {
			out = append(out, c)
		}
	}
	pl.Changes = out
}

// declaredImports filters imports down to those whose address has a matching
// resource block, so --generate-config can route the rest to generateConfig
// instead of letting ResolveImports reject them as not declared.
func declaredImports(imports []plan.ImportBlock, declared map[string]bool) []plan.ImportBlock {
	var out []plan.ImportBlock
	for _, imp := range imports {
		if declared[imp.Address] {
			out = append(out, imp)
		}
	}
	return out
}

// generateConfig writes HCL for each import whose address is NOT declared as a
// resource, by reading the live resource and emitting a resource block. It never
// touches state.
func generateConfig(ctx context.Context, p provider.Provider, imports []plan.ImportBlock, declared map[string]bool, outPath string, stdout io.Writer) error {
	resources := p.Resources()
	var blocks []*hclwrite.Block
	seen := map[string]bool{}
	for _, imp := range imports {
		if declared[imp.Address] {
			continue
		}
		if seen[imp.Address] {
			return fmt.Errorf("duplicate import block for %q", imp.Address)
		}
		seen[imp.Address] = true
		typ, name, _, hasKey, err := addr.Parse(imp.Address)
		if err != nil {
			return err
		}
		if hasKey {
			return fmt.Errorf("generate %s: --generate-config does not support for_each resources; declare the resource block by hand", imp.Address)
		}
		r, ok := resources[typ]
		if !ok {
			return fmt.Errorf("generate %s: unknown resource type %q", imp.Address, typ)
		}
		actual, err := r.Read(ctx, imp.ID)
		if err != nil {
			if errors.Is(err, provider.ErrNotFound) {
				return fmt.Errorf("generate %s: no %s found with id %q", imp.Address, typ, imp.ID)
			}
			return err
		}
		block, err := configgen.Generate(r, name, imp.ID, actual)
		if err != nil {
			return err
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return nil
	}
	if err := configgen.WriteFile(outPath, blocks); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Generated configuration for %d resource(s) → %s\n", len(blocks), outPath)
	return nil
}
