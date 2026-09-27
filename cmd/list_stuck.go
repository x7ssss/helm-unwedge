package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/x7ssss/helm-unwedge/pkg/k8s"
	"github.com/x7ssss/helm-unwedge/pkg/ui"
)

var (
	listAllNamespaces bool
	listStaleAfter    time.Duration
)

func newListStuckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-stuck",
		Short: "Scan cluster or namespace for deadlocked Helm releases",
		Long: `list-stuck inspects Helm release storage secrets across the cluster or a specified
namespace to discover releases stuck in transition states (pending-upgrade, pending-install,
pending-rollback). It reports the revision, elapsed stuck duration, and staleness classification.`,
		RunE: runListStuck,
	}

	cmd.Flags().BoolVarP(&listAllNamespaces, "all-namespaces", "A", false, "Scan across all Kubernetes namespaces")
	cmd.Flags().DurationVar(&listStaleAfter, "stale-after", 10*time.Minute, "Time threshold to classify a pending release as stale")

	return cmd
}

func runListStuck(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	client, defaultNS, err := k8s.NewClient(k8s.ClientConfig{
		KubeconfigPath: kubeconfigPath,
		ContextName:    kubeContext,
	})
	if err != nil {
		return fmt.Errorf("kubernetes client init failed: %w", err)
	}

	targetNS := namespace
	if listAllNamespaces {
		targetNS = "" // Empty namespace lists across all namespaces in client-go
	} else if targetNS == "" {
		targetNS = defaultNS
	}

	stuckReleases, err := k8s.ListAllStuckReleases(ctx, client, targetNS)
	if err != nil {
		return fmt.Errorf("failed to scan for stuck releases: %w", err)
	}

	table := ui.NewTable("Namespace", "Release", "Revision", "Status", "Last Modified", "Stuck For", "Staleness")
	now := time.Now().UTC()

	for _, rel := range stuckReleases {
		var age time.Duration
		ageStr := "unknown"
		staleness := "STALE"

		if !rel.ModifiedAt.IsZero() {
			age = now.Sub(rel.ModifiedAt).Round(time.Second)
			ageStr = age.String()
			if age < listStaleAfter {
				staleness = "ACTIVE (recent)"
			}
		}

		lastModStr := "unknown"
		if !rel.ModifiedAt.IsZero() {
			lastModStr = rel.ModifiedAt.Format("2006-01-02 15:04:05")
		}

		table.AddRow(
			rel.Namespace,
			rel.Name,
			fmt.Sprintf("v%d", rel.Revision),
			rel.Status,
			lastModStr,
			ageStr,
			staleness,
		)
	}

	headerNotice := targetNS
	if targetNS == "" {
		headerNotice = "all namespaces"
	}
	fmt.Fprintf(os.Stdout, "Found %d deadlocked release(s) in %s:\n\n", len(stuckReleases), headerNotice)
	table.Render(os.Stdout)

	return nil
}
