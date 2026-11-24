package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
)

type cleanupItem struct {
	Address    string    `json:"address"`
	Type       string    `json:"type"`
	ID         string    `json:"id"`
	ReplacedBy string    `json:"replaced_by"`
	RetiredAt  time.Time `json:"retired_at"`
}

type cleanupResult struct {
	// DryRun is true when nothing was archived: no keep list and no --force.
	DryRun bool `json:"dry_run"`
	// Archived were removed; Candidates would be (dry run); NotArchivable
	// can't be removed through the platform's API and stay recorded; Kept
	// are protected by the keep list or still managed, and Recent are newer
	// than --older-than.
	Archived      []cleanupItem `json:"archived"`
	Candidates    []cleanupItem `json:"candidates,omitempty"`
	NotArchivable []cleanupItem `json:"not_archivable,omitempty"`
	Kept          []cleanupItem `json:"kept,omitempty"`
	Recent        []cleanupItem `json:"recent,omitempty"`
}

func newCleanupCmd(p provider.Provider, cfgPath, statePath *string, jsonOut *bool, cmdName *string, s streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Archive objects that replaces left in place, once nothing uses them",
		Long: "A replace of a roblox_asset leaves the old asset in place, because servers on an older\n" +
			"build still load it, and records it in state. cleanup archives those recorded objects.\n\n" +
			"Without --keep-from, --keep or --force, it only lists what it would archive. Pass the ids\n" +
			"every deployed build still uses with --keep-from (every number in the file is kept, so\n" +
			"a generated Luau module works as is) or --keep. Ids the state still manages are always kept.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			*cmdName = cmd.Name()
			keepFiles, _ := cmd.Flags().GetStringArray("keep-from")
			keepIDs, _ := cmd.Flags().GetStringArray("keep")
			olderThan, _ := cmd.Flags().GetDuration("older-than")
			force, _ := cmd.Flags().GetBool("force")
			return cleanupRun(p, *cfgPath, *statePath, keepFiles, keepIDs, olderThan, force, autoApproved(cmd), fmtOf(*jsonOut), s)
		},
	}
	cmd.Flags().StringArray("keep-from", nil, "keep every id that appears in this file (repeatable)")
	cmd.Flags().StringArray("keep", nil, "keep this id (repeatable)")
	cmd.Flags().Duration("older-than", 0, "only archive objects replaced at least this long ago, e.g. 24h")
	cmd.Flags().Bool("force", false, "archive without a keep list")
	cmd.Flags().Bool("auto-approve", false, "skip the interactive approval prompt")
	cmd.Flags().Bool("yes", false, "alias for --auto-approve")
	return cmd
}

// digitRuns and wordRuns find what in a --keep-from file could be an id:
// every run of digits (Roblox ids, wherever they appear in a Luau module or
// JSON), and every run of word characters, so other id formats can be kept
// too. Keeping a number that isn't an id is harmless.
var (
	digitRuns = regexp.MustCompile(`[0-9]+`)
	wordRuns  = regexp.MustCompile(`[A-Za-z0-9_./-]+`)
)

func keepSet(files, ids []string) (map[string]bool, error) {
	keep := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("--keep-from: %w", err)
		}
		for _, re := range []*regexp.Regexp{digitRuns, wordRuns} {
			for _, tok := range re.FindAllString(string(b), -1) {
				keep[tok] = true
			}
		}
	}
	for _, id := range ids {
		keep[strings.TrimSpace(id)] = true
	}
	return keep, nil
}

func cleanupRun(p provider.Provider, cfgPath, statePath string, keepFiles, keepIDs []string, olderThan time.Duration, force, autoApprove bool, format output.Format, s streams) error {
	keep, err := keepSet(keepFiles, keepIDs)
	if err != nil {
		return err
	}
	dryRun := len(keepFiles) == 0 && len(keepIDs) == 0 && !force
	b, _, err := openBackend(cfgPath, statePath)
	if err != nil {
		return err
	}
	unlock, err := b.Lock("cleanup")
	if err != nil {
		return err
	}
	defer releaseLock(unlock, s)
	st, err := b.Get()
	if err != nil {
		return err
	}

	res := cleanupResult{DryRun: dryRun, Archived: []cleanupItem{}}
	var todo []state.Retired
	cutoff := time.Now().Add(-olderThan)
	for _, r := range st.Retired {
		item := cleanupItem{Address: r.Address, Type: r.Type, ID: r.ID, ReplacedBy: r.ReplacedBy, RetiredAt: r.RetiredAt}
		_, managed := st.FindByID(r.Type, r.ID)
		switch {
		case keep[r.ID] || managed:
			res.Kept = append(res.Kept, item)
		case r.RetiredAt.After(cutoff):
			res.Recent = append(res.Recent, item)
		default:
			res.Candidates = append(res.Candidates, item)
			todo = append(todo, r)
		}
	}

	if dryRun || len(todo) == 0 {
		return output.Emit(s.out, format, "cleanup", renderCleanup(res), res)
	}
	for _, r := range todo {
		if _, ok := p.Resources()[r.Type]; !ok {
			return fmt.Errorf("cleanup: %s %s: unknown resource type %q (is ROBLOX_API_KEY set?)", r.Address, r.ID, r.Type)
		}
	}
	if format == output.Text {
		fmt.Fprint(s.out, renderCleanup(res))
	}
	ok, err := confirm(s, format, autoApprove, "cleanup")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(s.out, "%s cancelled.\n", cancelLabel["cleanup"])
		return errCancelled
	}
	res.Candidates = nil
	ctx := context.Background()
	for _, r := range todo {
		item := cleanupItem{Address: r.Address, Type: r.Type, ID: r.ID, ReplacedBy: r.ReplacedBy, RetiredAt: r.RetiredAt}
		archived, err := archive(ctx, p.Resources()[r.Type], r.ID)
		if err != nil {
			return fmt.Errorf("cleanup: archiving %s (id %s): %w", r.Address, r.ID, err)
		}
		if !archived {
			res.NotArchivable = append(res.NotArchivable, item)
			continue
		}
		res.Archived = append(res.Archived, item)
		forget(st, r)
		st.Serial++
		if err := b.Put(st); err != nil {
			return err
		}
	}
	text := fmt.Sprintf("Cleanup complete: %d archived.\n", len(res.Archived))
	if len(res.NotArchivable) > 0 {
		text += notArchivableText(res.NotArchivable)
	}
	return output.Emit(s.out, format, "cleanup", text, res)
}

// archive removes a retired object through the resource's Archiver, or its
// Delete when it has none.
func archive(ctx context.Context, r provider.Resource, id string) (bool, error) {
	if a, ok := r.(provider.Archiver); ok {
		return a.Archive(ctx, id)
	}
	if err := r.Delete(ctx, id); err != nil && !errors.Is(err, provider.ErrNotFound) {
		return false, err
	}
	return true, nil
}

func forget(st *state.State, r state.Retired) {
	out := st.Retired[:0]
	for _, x := range st.Retired {
		if x != r {
			out = append(out, x)
		}
	}
	st.Retired = out
}

func renderCleanup(res cleanupResult) string {
	var b strings.Builder
	line := func(it cleanupItem) {
		fmt.Fprintf(&b, "  %s  id %s (replaced by %s, %s)\n", it.Address, it.ID, it.ReplacedBy, it.RetiredAt.Format(time.RFC3339))
	}
	if len(res.Candidates) == 0 {
		b.WriteString("Nothing to clean up.\n")
	} else {
		if res.DryRun {
			fmt.Fprintf(&b, "Would archive %d object(s) that replaces left in place:\n", len(res.Candidates))
		} else {
			fmt.Fprintf(&b, "Will archive %d object(s) that replaces left in place:\n", len(res.Candidates))
		}
		for _, it := range res.Candidates {
			line(it)
		}
	}
	if len(res.Kept) > 0 {
		fmt.Fprintf(&b, "Keeping %d still in use (in the keep list, or managed by this state).\n", len(res.Kept))
	}
	if len(res.Recent) > 0 {
		fmt.Fprintf(&b, "Keeping %d replaced more recently than --older-than.\n", len(res.Recent))
	}
	if res.DryRun && len(res.Candidates) > 0 {
		b.WriteString("\nNothing was archived. Pass the ids every deployed build still uses with --keep-from <file>\n" +
			"or --keep <id> to archive the rest, or --force to archive without a keep list.\n")
	}
	return b.String()
}

func notArchivableText(items []cleanupItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d object(s) can't be archived through the API, so they stay recorded; archive them by hand (for Roblox assets, in Creator Hub):\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "  %s  id %s\n", it.Address, it.ID)
	}
	return b.String()
}
