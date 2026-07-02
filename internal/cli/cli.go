package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/outpostgroup/baseplate/internal/config"
	"github.com/outpostgroup/baseplate/internal/engine/apply"
	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

func newProvider() provider.Provider { return mock.New() }

func Execute() { os.Exit(run(os.Args[1:], os.Stdout)) }

func run(args []string, stdout io.Writer) int {
	var cfgPath, statePath, target string
	var jsonOut bool

	root := &cobra.Command{Use: "baseplate", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&cfgPath, "config", "baseplate.bp.hcl", "config file")
	root.PersistentFlags().StringVar(&statePath, "state", "baseplate.tfstate", "state file")
	root.PersistentFlags().StringVar(&target, "target", "", "limit to a single resource address")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	root.SetOut(stdout)
	root.SetArgs(args)

	exit := 0
	p := newProvider()

	load := func() ([]plan.DesiredResource, error) {
		_, res, err := config.Load(cfgPath, p)
		if err != nil {
			return nil, err
		}
		return filterTarget(res, target), nil
	}

	root.AddCommand(&cobra.Command{Use: "validate", RunE: func(_ *cobra.Command, _ []string) error {
		if _, err := load(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Configuration is valid.")
		return nil
	}})

	root.AddCommand(&cobra.Command{Use: "plan", RunE: func(_ *cobra.Command, _ []string) error {
		desired, err := load()
		if err != nil {
			return err
		}
		b := backend.NewLocal(statePath)
		s, err := b.Get()
		if err != nil {
			return err
		}
		pl, err := plan.Build(context.Background(), p, s, desired)
		if err != nil {
			return err
		}
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
	}})

	root.AddCommand(&cobra.Command{Use: "apply", RunE: func(_ *cobra.Command, _ []string) error {
		return applyRun(p, cfgPath, statePath, target, false, stdout)
	}})
	root.AddCommand(&cobra.Command{Use: "destroy", RunE: func(_ *cobra.Command, _ []string) error {
		return applyRun(p, cfgPath, statePath, target, true, stdout)
	}})

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return exit
}

func applyRun(p provider.Provider, cfgPath, statePath, target string, destroy bool, stdout io.Writer) error {
	var desired []plan.DesiredResource
	if !destroy {
		_, res, err := config.Load(cfgPath, p)
		if err != nil {
			return err
		}
		desired = filterTarget(res, target)
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
	if err := apply.Apply(context.Background(), p, b, s, pl); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Apply complete. %d change(s).\n", len(pl.Changes))
	return nil
}

func filterTarget(res []plan.DesiredResource, target string) []plan.DesiredResource {
	if target == "" {
		return res
	}
	var out []plan.DesiredResource
	for _, r := range res {
		if r.Address() == target {
			out = append(out, r)
		}
	}
	return out
}
