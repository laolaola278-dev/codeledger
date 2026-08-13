package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/reportgen"
	"github.com/spf13/cobra"
)

func newReportCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("report", "Generate a progress report",
		`Generate a Markdown progress report and save it to .ctask/reports/.

The report includes project overview, completed tasks, in-progress tasks,
blocked tasks, modified files, test results, risks, and next steps.`)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		report, err := reportgen.GenerateReport(s)
		if err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "report generation failed")
		}

		// Save to reports/YYYY-MM-DD-report.md
		date := time.Now().Format("2006-01-02")
		reportPath := filepath.Join(s.ReportsDirPath(), date+"-report.md")

		if err := os.MkdirAll(s.ReportsDirPath(), 0755); err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "failed to create reports directory")
		}

		if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "failed to write report")
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Report saved to: %s\n", reportPath)
		return nil
	}
	return cmd
}
