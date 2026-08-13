package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/codeledger/codeledger/internal/reportgen"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
	"time"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate a progress report",
	Long: `Generate a Markdown progress report and save it to .ctask/reports/.

The report includes project overview, completed tasks, in-progress tasks,
blocked tasks, modified files, test results, risks, and next steps.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		report, err := reportgen.GenerateReport(s)
		if err != nil {
			return fmt.Errorf("report generation failed: %w", err)
		}

		// Save to reports/YYYY-MM-DD-report.md
		date := time.Now().Format("2006-01-02")
		reportPath := filepath.Join(s.ReportsDirPath(), date+"-report.md")

		if err := os.MkdirAll(s.ReportsDirPath(), 0755); err != nil {
			return fmt.Errorf("failed to create reports directory: %w", err)
		}

		if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
			return fmt.Errorf("failed to write report: %w", err)
		}

		fmt.Printf("Report saved to: %s\n", reportPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reportCmd)
}
