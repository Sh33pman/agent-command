package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/report"
	"github.com/brettmostert/agent-command/internal/version"
)

func newReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Report a bug or request a feature",
		Long: `Prepares a GitHub issue on ` + report.Repo + `, which you submit on the page.
A bug report can include the last 200 lines of the daemon log, with secrets, your
account name and your home folder taken out. ` + "`acmd report details`" + ` shows what
would be added.`,
	}
	cmd.AddCommand(newReportDetailsCmd(), newReportPageCmd())
	return cmd
}

func reportKind(kind string) error {
	if kind != "bug" && kind != "feature" {
		return fmt.Errorf("--kind is bug or feature, not %q", kind)
	}
	return nil
}

func newReportDetailsCmd() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "details",
		Short: "Print what a report adds about this install",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := reportKind(kind); err != nil {
				return err
			}
			p, err := paths.Default()
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), report.Details(kind, version.Get().Version, macOSVersion(cmd.Context()), p.DaemonLog(), report.NewRedactor()))
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "bug", "bug or feature")
	return cmd
}

// reportPage is GitHub's page with the report filled in, for the app to open.
type reportPage struct {
	PageURL string `json:"page_url"`
	// Body is the full cleaned text, and Cut says the page holds less than that.
	Body string `json:"body"`
	Cut  bool   `json:"cut,omitempty"`
}

func newReportPageCmd() *cobra.Command {
	var kind, title string
	cmd := &cobra.Command{
		Use:   "page",
		Short: "Make the GitHub page for a report, from the body on standard input",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := reportKind(kind); err != nil {
				return err
			}
			title = strings.TrimSpace(title)
			if title == "" {
				return errors.New("a report needs a title")
			}
			in, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
			if err != nil {
				return err
			}
			// Everything is cleaned again here: this is the last stop before the browser.
			r := report.NewRedactor()
			body := r.Text(string(in))
			page, fits := report.IssueURL(kind, r.Text(title), body)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(reportPage{PageURL: page, Body: body, Cut: !fits})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "bug", "bug or feature")
	cmd.Flags().StringVar(&title, "title", "", "the issue's title")
	return cmd
}

func macOSVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
