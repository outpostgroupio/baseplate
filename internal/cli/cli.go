package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/cobra"

	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/config/configgen"
	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
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

type validateResult struct {
	Valid bool `json:"valid"`
}

type planResult struct {
	Changes []plan.JSONChange `json:"changes"`
	Summary plan.Summary      `json:"summary"`
}

type importResult struct {
	Address string `json:"address"`
	ID      string `json:"id"`
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
	root.PersistentFlags().StringVar(&cfgPath, "config", "baseplate.bp.hcl", "config file")
	root.PersistentFlags().StringVar(&statePath, "state", "baseplate.bpstate", "state file")
	root.PersistentFlags().StringVar(&target, "target", "", "limit to a single resource address")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	root.SetOut(s.out)
	root.SetErr(s.err)
	root.SetArgs(args)

	exit := 0
	p := newProvider()

	load := func() ([]plan.DesiredResource, []plan.ImportBlock, error) {
		_, res, imports, err := config.Load(cfgPath, p)
		if err != nil {
			return nil, nil, err
		}
		return res, imports, nil
	}

	root.AddCommand(&cobra.Command{Use: "validate", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		if _, _, err := load(); err != nil {
			return err
		}
		return output.Emit(s.out, fmtOf(jsonOut), "validate", "Configuration is valid.\n", validateResult{Valid: true})
	}})

	planCmd := &cobra.Command{Use: "plan", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		desired, imports, err := load()
		if err != nil {
			return err
		}
		b := backend.NewLocal(statePath)
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
		if err := plan.ResolveImports(context.Background(), p, st, pl, toResolve); err != nil {
			return err
		}
		if generateConfigOut != "" {
			if err := generateConfig(context.Background(), p, imports, declared, generateConfigOut, s.out); err != nil {
				return err
			}
		}
		filterPlanByTarget(pl, target)
		if err := output.Emit(s.out, fmtOf(jsonOut), "plan",
			pl.RenderText(true),
			planResult{Changes: pl.JSONChanges(), Summary: pl.Summary()}); err != nil {
			return err
		}
		if pl.HasChanges() {
			exit = 2
		}
		return nil
	}}
	planCmd.Flags().StringVar(&generateConfigOut, "generate-config", "", "write generated HCL for undeclared import blocks to this file")
	root.AddCommand(planCmd)

	root.AddCommand(&cobra.Command{Use: "apply", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		return applyRun(p, cfgPath, statePath, target, false, s)
	}})
	root.AddCommand(&cobra.Command{Use: "destroy", RunE: func(cmd *cobra.Command, _ []string) error {
		cmdName = cmd.Name()
		return applyRun(p, cfgPath, statePath, target, true, s)
	}})
	root.AddCommand(&cobra.Command{
		Use:   "import <address> <id>",
		Short: "Adopt an existing resource into state",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmdName = cmd.Name()
			return importRun(p, cfgPath, statePath, args[0], args[1], fmtOf(jsonOut), s)
		},
	})

	if err := root.Execute(); err != nil {
		output.EmitError(s.err, fmtOf(jsonOut), cmdName, err)
		return 1
	}
	return exit
}

func applyRun(p provider.Provider, cfgPath, statePath, target string, destroy bool, s streams) error {
	var desired []plan.DesiredResource
	var imports []plan.ImportBlock
	if !destroy {
		_, res, imps, err := config.Load(cfgPath, p)
		if err != nil {
			return err
		}
		desired, imports = res, imps
	}
	b := backend.NewLocal(statePath)
	unlock, err := b.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	st, err := b.Get()
	if err != nil {
		return err
	}
	pl, err := plan.Build(context.Background(), p, st, desired)
	if err != nil {
		return err
	}
	if err := plan.ResolveImports(context.Background(), p, st, pl, imports); err != nil {
		return err
	}
	filterPlanByTarget(pl, target)
	if err := apply.Apply(context.Background(), p, b, st, pl); err != nil {
		return err
	}
	fmt.Fprintf(s.out, "Apply complete. %d change(s).\n", len(pl.Changes))
	return nil
}

// importRun adopts an existing resource (declared in config) into state by id,
// under the state lock. It never mutates the remote.
func importRun(p provider.Provider, cfgPath, statePath, address, id string, format output.Format, s streams) error {
	_, desired, _, err := config.Load(cfgPath, p)
	if err != nil {
		return err
	}
	b := backend.NewLocal(statePath)
	unlock, err := b.Lock()
	if err != nil {
		return err
	}
	defer unlock()
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

// filterPlanByTarget restricts the plan to a single resource address when
// --target is set, so targeted operations never touch (or delete) other resources.
func filterPlanByTarget(pl *plan.Plan, target string) {
	if target == "" {
		return
	}
	var out []plan.Change
	for _, c := range pl.Changes {
		if c.Address == target {
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
		typ, name := splitAddress(imp.Address)
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

func splitAddress(addr string) (typ, name string) {
	if i := strings.IndexByte(addr, '.'); i >= 0 {
		return addr[:i], addr[i+1:]
	}
	return addr, ""
}
