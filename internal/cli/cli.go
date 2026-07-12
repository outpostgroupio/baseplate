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

	"github.com/outpostgroup/baseplate/internal/config"
	"github.com/outpostgroup/baseplate/internal/config/configgen"
	"github.com/outpostgroup/baseplate/internal/engine/apply"
	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
	"github.com/outpostgroup/baseplate/internal/provider/roblox"
	"github.com/outpostgroup/baseplate/internal/state/backend"
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

func Execute() { os.Exit(run(os.Args[1:], os.Stdout)) }

func run(args []string, stdout io.Writer) int {
	var cfgPath, statePath, target string
	var jsonOut bool
	var generateConfigOut string

	root := &cobra.Command{Use: "baseplate", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&cfgPath, "config", "baseplate.bp.hcl", "config file")
	root.PersistentFlags().StringVar(&statePath, "state", "baseplate.bpstate", "state file")
	root.PersistentFlags().StringVar(&target, "target", "", "limit to a single resource address")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	root.SetOut(stdout)
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

	root.AddCommand(&cobra.Command{Use: "validate", RunE: func(_ *cobra.Command, _ []string) error {
		if _, _, err := load(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Configuration is valid.")
		return nil
	}})

	planCmd := &cobra.Command{Use: "plan", RunE: func(_ *cobra.Command, _ []string) error {
		desired, imports, err := load()
		if err != nil {
			return err
		}
		b := backend.NewLocal(statePath)
		s, err := b.Get()
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
		pl, err := plan.Build(context.Background(), p, s, desired)
		if err != nil {
			return err
		}
		if err := plan.ResolveImports(context.Background(), p, s, pl, toResolve); err != nil {
			return err
		}
		if generateConfigOut != "" {
			if err := generateConfig(context.Background(), p, imports, declared, generateConfigOut, stdout); err != nil {
				return err
			}
		}
		filterPlanByTarget(pl, target)
		if jsonOut {
			out, _ := pl.RenderJSON()
			fmt.Fprintln(stdout, string(out))
		} else {
			fmt.Fprint(stdout, pl.RenderText(true))
		}
		if pl.HasChanges() {
			exit = 2
		}
		return nil
	}}
	planCmd.Flags().StringVar(&generateConfigOut, "generate-config", "", "write generated HCL for undeclared import blocks to this file")
	root.AddCommand(planCmd)

	root.AddCommand(&cobra.Command{Use: "apply", RunE: func(_ *cobra.Command, _ []string) error {
		return applyRun(p, cfgPath, statePath, target, false, stdout)
	}})
	root.AddCommand(&cobra.Command{Use: "destroy", RunE: func(_ *cobra.Command, _ []string) error {
		return applyRun(p, cfgPath, statePath, target, true, stdout)
	}})
	root.AddCommand(&cobra.Command{
		Use:   "import <address> <id>",
		Short: "Adopt an existing resource into state",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return importRun(p, cfgPath, statePath, args[0], args[1], stdout)
		},
	})

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return exit
}

func applyRun(p provider.Provider, cfgPath, statePath, target string, destroy bool, stdout io.Writer) error {
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
	s, err := b.Get()
	if err != nil {
		return err
	}
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		return err
	}
	if err := plan.ResolveImports(context.Background(), p, s, pl, imports); err != nil {
		return err
	}
	filterPlanByTarget(pl, target)
	if err := apply.Apply(context.Background(), p, b, s, pl); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Apply complete. %d change(s).\n", len(pl.Changes))
	return nil
}

// importRun adopts an existing resource (declared in config) into state by id,
// under the state lock. It never mutates the remote.
func importRun(p provider.Provider, cfgPath, statePath, address, id string, stdout io.Writer) error {
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
	s, err := b.Get()
	if err != nil {
		return err
	}
	if err := apply.Import(context.Background(), p, b, s, address, id, desired); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Imported %s (id %s).\n", address, id)
	return nil
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
	for _, imp := range imports {
		if declared[imp.Address] {
			continue
		}
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
