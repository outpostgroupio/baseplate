package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// Resource types the restart commands look at. The CLI stays provider-agnostic
// for the restart itself (provider.ServerRestarter); these names are only used
// to find the universe in config and to detect place publishes in a plan.
const (
	experienceType = "roblox_experience"
	placeType      = "roblox_place"
)

// restartResult is the JSON shape of one restart request (the `restart-servers`
// result, and each entry of apply's `restarts`).
type restartResult struct {
	UniverseID string `json:"universe_id"`
	// PlaceIDs is empty when every place in the universe is targeted.
	PlaceIDs        []string `json:"place_ids"`
	AllVersions     bool     `json:"all_versions"`
	BleedOffMinutes int      `json:"bleed_off_minutes"`
}

func newRestartResult(req provider.RestartRequest) restartResult {
	ids := req.PlaceIDs
	if ids == nil {
		ids = []string{}
	}
	return restartResult{
		UniverseID:      req.UniverseID,
		PlaceIDs:        ids,
		AllVersions:     req.AllVersions,
		BleedOffMinutes: int(req.BleedOff / time.Minute),
	}
}

// serverRestarter returns the provider's restart capability, or an error that
// tells the user why it's missing (the credential-less mock doesn't have one).
func serverRestarter(p provider.Provider) (provider.ServerRestarter, error) {
	rs, ok := p.(provider.ServerRestarter)
	if !ok {
		return nil, errors.New("restarting servers needs the Roblox provider: set ROBLOX_API_KEY (with the universe:write scope)")
	}
	return rs, nil
}

// describeRestart spells out exactly what a restart request will do, for the
// confirmation prompt. Restarts disrupt live players, so this is deliberately
// explicit.
func describeRestart(req provider.RestartRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Baseplate will restart live game servers in universe %s:\n", req.UniverseID)
	if len(req.PlaceIDs) == 0 {
		b.WriteString("  places:   every place in the universe\n")
	} else {
		fmt.Fprintf(&b, "  places:   only place(s) %s\n", strings.Join(req.PlaceIDs, ", "))
	}
	if req.AllVersions {
		b.WriteString("  servers:  ALL running servers, including ones already on the latest version\n")
	} else {
		b.WriteString("  servers:  only servers running an older place version than the latest published one\n" +
			"            (if nothing newer has been published, no servers are restarted)\n")
	}
	if req.BleedOff > 0 {
		m := int(req.BleedOff / time.Minute)
		fmt.Fprintf(&b, "  shutdown: after a %d-minute bleed-off: no new players are matched into these servers\n"+
			"            from now on, and any players still in them after %d minute(s) are removed\n", m, m)
	} else {
		b.WriteString("  shutdown: IMMEDIATELY: every player in these servers is removed from the game now\n")
	}
	return b.String()
}

// newRestartServersCmd builds `baseplate restart-servers`.
func newRestartServersCmd(p provider.Provider, cfgPath *string, jsonOut *bool, cmdName *string, s streams) *cobra.Command {
	var opts struct {
		universe    string
		places      []string
		allVersions bool
		bleedOff    time.Duration
	}
	cmd := &cobra.Command{
		Use:   "restart-servers",
		Short: "Restart an experience's live game servers onto the latest published version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			*cmdName = cmd.Name()
			req := provider.RestartRequest{
				UniverseID:  opts.universe,
				PlaceIDs:    opts.places,
				AllVersions: opts.allVersions,
				BleedOff:    opts.bleedOff,
			}
			return restartServersRun(p, *cfgPath, req, autoApproved(cmd), fmtOf(*jsonOut), s)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.universe, "universe", "", "universe id (default: the roblox_experience universe_id in --config)")
	f.StringSliceVar(&opts.places, "place", nil, "restrict to this place id (repeatable; default: all places)")
	f.BoolVar(&opts.allVersions, "all-versions", false, "restart servers on every version, not just outdated ones")
	f.DurationVar(&opts.bleedOff, "bleed-off", 0, "stop matchmaking, then shut servers down after this long (whole minutes, 1m-60m; default: immediately)")
	f.Bool("auto-approve", false, "skip the interactive approval prompt")
	f.Bool("yes", false, "alias for --auto-approve")
	return cmd
}

// restartServersRun validates the request, resolves the universe, confirms,
// and asks the provider to restart. req.UniverseID may be empty, in which case
// it is derived from cfgPath.
func restartServersRun(p provider.Provider, cfgPath string, req provider.RestartRequest, autoApprove bool, format output.Format, s streams) error {
	rs, err := serverRestarter(p)
	if err != nil {
		return err
	}
	if err := rs.ValidateRestart(req); err != nil {
		return err
	}
	if req.UniverseID == "" {
		if req.UniverseID, err = universeFromConfig(cfgPath, p); err != nil {
			return err
		}
	}
	if format == output.Text {
		fmt.Fprint(s.out, describeRestart(req))
	}
	ok, err := confirm(s, format, autoApprove, "restart-servers")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(s.out, "%s cancelled.\n", cancelLabel["restart-servers"])
		return errCancelled
	}
	if err := rs.RestartServers(context.Background(), req); err != nil {
		return err
	}
	return output.Emit(s.out, format, "restart-servers",
		fmt.Sprintf("Restart requested for universe %s. Roblox restarts the servers asynchronously.\n", req.UniverseID),
		newRestartResult(req))
}

// universeFromConfig derives the universe id from the config's
// roblox_experience resource(s). It must be unambiguous: exactly one distinct
// literal universe_id.
func universeFromConfig(cfgPath string, p provider.Provider) (string, error) {
	if _, err := os.Stat(cfgPath); err != nil {
		return "", fmt.Errorf("no --universe given and config %s could not be read (%v); pass --universe", cfgPath, err)
	}
	cfg, err := config.LoadConfigWith(cfgPath, p, config.LoadOptions{RemoteState: remoteStateReader()})
	if err != nil {
		return "", err
	}
	desired := cfg.Resources
	seen := map[string]bool{}
	var ids []string
	for _, d := range desired {
		if d.Type != experienceType {
			continue
		}
		v, ok := d.Attributes["universe_id"]
		if !ok || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return "", fmt.Errorf("%s has no literal universe_id; pass --universe", d.Address())
		}
		if id := v.AsString(); !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no %s in %s to take the universe from; pass --universe", experienceType, cfgPath)
	case 1:
		return ids[0], nil
	default:
		sort.Strings(ids)
		return "", fmt.Errorf("%s declares several universes (%s); pass --universe", cfgPath, strings.Join(ids, ", "))
	}
}

// applyRestart is the `apply --restart-servers` option set. A nil
// *applyRestart means no restart was requested.
type applyRestart struct {
	bleedOff time.Duration
}

func addApplyRestartFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("restart-servers", false, "after a successful apply that published a roblox_place, restart the servers running older versions of it")
	cmd.Flags().Duration("restart-bleed-off", 0, "with --restart-servers: bleed-off before servers shut down (whole minutes, 1m-60m; default: immediately)")
}

func applyRestartFromFlags(cmd *cobra.Command) (*applyRestart, error) {
	on, _ := cmd.Flags().GetBool("restart-servers")
	bleed, _ := cmd.Flags().GetDuration("restart-bleed-off")
	if !on {
		if cmd.Flags().Changed("restart-bleed-off") {
			return nil, errors.New("--restart-bleed-off requires --restart-servers")
		}
		return nil, nil
	}
	return &applyRestart{bleedOff: bleed}, nil
}

// check fails fast, before anything is planned or applied, if the restart
// can't happen (no capability, invalid options).
func (r *applyRestart) check(p provider.Provider) error {
	rs, err := serverRestarter(p)
	if err != nil {
		return fmt.Errorf("--restart-servers: %w", err)
	}
	if err := rs.ValidateRestart(provider.RestartRequest{BleedOff: r.bleedOff}); err != nil {
		return fmt.Errorf("--restart-servers: %w", err)
	}
	return nil
}

// publishedPlaces returns the plan's roblox_place changes that publish a new
// version (Create or Update), in plan order.
func publishedPlaces(pl *plan.Plan) []plan.Change {
	var out []plan.Change
	for _, c := range pl.Changes {
		if c.Type == placeType && (c.Action == provider.ActionCreate || c.Action == provider.ActionUpdate) {
			out = append(out, c)
		}
	}
	return out
}

// describe is shown before the apply confirmation so the prompt covers the
// restart too.
func (r *applyRestart) describe(pl *plan.Plan) string {
	places := publishedPlaces(pl)
	if len(places) == 0 {
		return "\n--restart-servers: no roblox_place is published by this plan, so no servers will be restarted.\n"
	}
	var b strings.Builder
	b.WriteString("\n--restart-servers: if the apply succeeds, Baseplate will then restart live game servers\n" +
		"running an older version of each published place:\n")
	for _, c := range places {
		fmt.Fprintf(&b, "  - %s\n", c.Address)
	}
	if r.bleedOff > 0 {
		m := int(r.bleedOff / time.Minute)
		fmt.Fprintf(&b, "No new players are matched into those servers; players still in them after %d minute(s) are removed.\n", m)
	} else {
		b.WriteString("Those servers shut down IMMEDIATELY: every player in them is removed from the game.\n")
	}
	return b.String()
}

// run restarts servers for the places the (successfully applied) plan
// published, one request per universe, scoped to the published place ids.
// Universe/place ids come from state, where apply-time references are resolved.
func (r *applyRestart) run(ctx context.Context, p provider.Provider, st *state.State, pl *plan.Plan) ([]restartResult, error) {
	rs, err := serverRestarter(p)
	if err != nil {
		return nil, err
	}
	byUniverse := map[string][]string{}
	var order []string
	for _, c := range publishedPlaces(pl) {
		res, ok := st.Find(c.Address)
		if !ok {
			return nil, fmt.Errorf("%s: not in state after apply", c.Address)
		}
		uid, err := stateString(res, "universe_id")
		if err != nil {
			return nil, err
		}
		pid, err := stateString(res, "place_id")
		if err != nil {
			return nil, err
		}
		if _, ok := byUniverse[uid]; !ok {
			order = append(order, uid)
		}
		byUniverse[uid] = append(byUniverse[uid], pid)
	}
	var results []restartResult
	for _, uid := range order {
		req := provider.RestartRequest{UniverseID: uid, PlaceIDs: byUniverse[uid], BleedOff: r.bleedOff}
		if err := rs.RestartServers(ctx, req); err != nil {
			return results, fmt.Errorf("universe %s: %w", uid, err)
		}
		results = append(results, newRestartResult(req))
	}
	return results, nil
}

func stateString(r *state.Resource, attr string) (string, error) {
	raw, ok := r.Attributes[attr]
	if !ok {
		return "", fmt.Errorf("%s: %s missing from state", r.Address(), attr)
	}
	v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType)
	if err != nil || v.IsNull() || v.Type() != cty.String {
		return "", fmt.Errorf("%s: %s in state is not a string", r.Address(), attr)
	}
	return v.AsString(), nil
}

// restartText is apply's human summary of the restarts it requested.
func restartText(results []restartResult) string {
	if len(results) == 0 {
		return "No roblox_place was published; servers were not restarted.\n"
	}
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "Restart requested for universe %s (place(s) %s).\n", r.UniverseID, strings.Join(r.PlaceIDs, ", "))
	}
	return b.String()
}
