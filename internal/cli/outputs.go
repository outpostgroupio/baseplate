package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// outputResult is one output value in --json, in Terraform's
// `terraform output -json` shape. Value is included even when sensitive, as
// there.
type outputResult struct {
	Value     json.RawMessage `json:"value"`
	Type      json.RawMessage `json:"type"`
	Sensitive bool            `json:"sensitive"`
}

func outputResults(outputs map[string]state.Output) map[string]outputResult {
	if len(outputs) == 0 {
		return nil
	}
	out := make(map[string]outputResult, len(outputs))
	for name, o := range outputs {
		out[name] = outputResult{Value: o.Value, Type: o.Type, Sensitive: o.Sensitive}
	}
	return out
}

// outputsText lists the recorded outputs as `name = value`, a sensitive one
// as <sensitive>, after an "Outputs:" heading; "" when there are none.
func outputsText(outputs map[string]state.Output) string {
	if len(outputs) == 0 {
		return ""
	}
	names := make([]string, 0, len(outputs))
	for n := range outputs {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("\nOutputs:\n\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s = %s\n", n, outputText(outputs[n], false))
	}
	return b.String()
}

// outputText renders one recorded output; reveal shows a sensitive value.
func outputText(o state.Output, reveal bool) string {
	if o.Sensitive && !reveal {
		return "<sensitive>"
	}
	v, err := o.CtyValue()
	if err != nil {
		return string(o.Value)
	}
	return plan.OutputString(v)
}

func newOutputCmd(cfgPath, statePath *string, jsonOut *bool, cmdName *string, s streams) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "output [name]",
		Short: "Print the output values recorded in state",
		Long: "Print the output values the last apply recorded in state. With a name, prints that\n" +
			"value, even when it is sensitive; --raw prints a string without quotes, for scripts.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			*cmdName = cmd.Name()
			b, _, err := openBackend(*cfgPath, *statePath)
			if err != nil {
				return err
			}
			st, err := b.Get()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if raw {
					return fmt.Errorf("--raw needs an output name")
				}
				text := strings.TrimPrefix(outputsText(st.Outputs), "\nOutputs:\n\n")
				res := outputResults(st.Outputs)
				if res == nil {
					res = map[string]outputResult{}
				}
				return output.Emit(s.out, fmtOf(*jsonOut), "output", text, res)
			}
			o, ok := st.Outputs[args[0]]
			if !ok {
				return fmt.Errorf("output %q not found in state (recorded: %s)", args[0], outputNames(st.Outputs))
			}
			text := outputText(o, true)
			if raw {
				v, err := o.CtyValue()
				if err != nil || v.IsNull() || v.Type() != cty.String {
					return fmt.Errorf("--raw only prints a string output; %q isn't one", args[0])
				}
				text = v.AsString()
			}
			return output.Emit(s.out, fmtOf(*jsonOut), "output", text+"\n", outputResult{Value: o.Value, Type: o.Type, Sensitive: o.Sensitive})
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "print a string value without quotes")
	return cmd
}

func outputNames(outputs map[string]state.Output) string {
	if len(outputs) == 0 {
		return "none"
	}
	names := make([]string, 0, len(outputs))
	for n := range outputs {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
