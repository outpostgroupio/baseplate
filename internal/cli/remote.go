package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/outpostgroupio/baseplate/internal/auth"
	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// openBrowser opens a URL in the user's browser. It is a variable so tests
// can play the browser.
var openBrowser = func(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// localStateOverride is set for one run when --state is given with a config
// that has a backend block: the command uses that local file instead of the
// backend (see stateWithBackend). run resets it.
var localStateOverride bool

// stateWithBackend decides what --state means when the config has a backend
// block. For apply, destroy and cleanup it's refused: they change real
// resources based on the state, and applying against a stray local copy
// creates duplicates.
// Other commands (plan, import, show) use the local file instead of the
// backend, for trying things out without touching the shared state, and say
// so on stderr.
func stateWithBackend(cmd *cobra.Command, cfgPath, statePath string, stderr io.Writer) error {
	f := cmd.Flags().Lookup("state")
	if f == nil || !f.Changed {
		return nil
	}
	bc, err := config.LoadBackend(cfgPath)
	if err != nil || bc == nil {
		return nil // no backend (or a config error, reported by the command)
	}
	switch cmd.Name() {
	case "apply", "destroy":
		return fmt.Errorf("--state can't be used with %s: the %q backend in %s stores the state, and applying against another copy would create duplicates. Remove --state", cmd.Name(), bc.Type, cfgPath)
	case "cleanup":
		return fmt.Errorf("--state can't be used with cleanup: the %q backend in %s records the objects to clean up. Remove --state", bc.Type, cfgPath)
	}
	localStateOverride = true
	fmt.Fprintf(stderr, "note: using the local state %s instead of the %q backend in %s\n", statePath, bc.Type, cfgPath)
	return nil
}

type forceUnlockResult struct {
	ID string `json:"id"`
}

func newForceUnlockCmd(cfgPath, statePath *string, jsonOut *bool, cmdName *string, s streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "force-unlock <id>",
		Short: "Release a state lock left behind by a run that is no longer active",
		Long: "Release the state lock with the given id, e.g. one left by a cancelled CI job. The id\n" +
			"is in the error a blocked command prints. Only do this when the run holding the lock\n" +
			"has stopped: releasing a live run's lock lets two runs write state at once.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			*cmdName = cmd.Name()
			b, _, err := openBackend(*cfgPath, *statePath)
			if err != nil {
				return err
			}
			fu, ok := b.(backend.ForceUnlocker)
			if !ok {
				return errors.New("force-unlock: local state locks are released when the process holding them exits, so there is nothing to force-unlock")
			}
			force, _ := cmd.Flags().GetBool("force")
			ok, err = confirm(s, fmtOf(*jsonOut), force, "force-unlock")
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(s.out, "%s cancelled.\n", cancelLabel["force-unlock"])
				return errCancelled
			}
			if err := fu.ForceUnlock(args[0]); err != nil {
				return err
			}
			return output.Emit(s.out, fmtOf(*jsonOut), "force-unlock",
				fmt.Sprintf("Released the state lock %s.\n", args[0]), forceUnlockResult{ID: args[0]})
		},
	}
	cmd.Flags().Bool("force", false, "skip the confirmation prompt")
	return cmd
}

// loginHost returns the host to sign in to: the argument, or the host of the
// config's http backend.
func loginHost(args []string, cfgPath string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	bc, err := config.LoadBackend(cfgPath)
	if err != nil {
		return "", err
	}
	if bc == nil || bc.Type != "http" {
		return "", fmt.Errorf("no host given, and %s has no http backend to take one from; run baseplate login <host>", cfgPath)
	}
	hc, err := httpConfigFromAttrs(bc.Attributes)
	if err != nil {
		return "", err
	}
	return auth.HostOf(hc.Address)
}

type loginResult struct {
	Host string `json:"host"`
}

func newLoginCmd(cfgPath *string, jsonOut *bool, cmdName *string, s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "login [host]",
		Short: "Sign in to a remote state server and store the token",
		Long: "Sign in to a state server (such as the Cloudflare Worker in workers/state) in your\n" +
			"browser, and store the token it issues for later commands. Without a host, signs in\n" +
			"to the host of the config's http backend.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			*cmdName = cmd.Name()
			host, err := loginHost(args, *cfgPath)
			if err != nil {
				return err
			}
			tok, err := auth.Login(context.Background(), host, auth.LoginOptions{
				Client: http.DefaultClient, OpenBrowser: openBrowser, Out: s.err,
			})
			if err != nil {
				return err
			}
			if err := auth.StoreToken(host, tok); err != nil {
				return err
			}
			path, _ := auth.CredentialsPath()
			return output.Emit(s.out, fmtOf(*jsonOut), "login",
				fmt.Sprintf("Signed in to %s. The token is stored in %s.\n", host, path), loginResult{Host: host})
		},
	}
}

func newLogoutCmd(cfgPath *string, jsonOut *bool, cmdName *string, s streams) *cobra.Command {
	return &cobra.Command{
		Use:   "logout [host]",
		Short: "Remove the stored token for a remote state server",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			*cmdName = cmd.Name()
			host, err := loginHost(args, *cfgPath)
			if err != nil {
				return err
			}
			if err := auth.StoreToken(host, ""); err != nil {
				return err
			}
			return output.Emit(s.out, fmtOf(*jsonOut), "logout",
				fmt.Sprintf("Removed the stored token for %s.\n", host), loginResult{Host: host})
		},
	}
}
