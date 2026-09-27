package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/x7ssss/helm-unwedge/pkg/codec"
	"github.com/x7ssss/helm-unwedge/pkg/k8s"
	"github.com/x7ssss/helm-unwedge/pkg/ui"
)

var (
	healStrategy   string
	healForce      bool
	healDryRun     bool
	healStaleAfter time.Duration
)

func newHealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "heal <release>",
		Short: "Surgically unlock a deadlocked Helm v3 release",
		Long: `Heal safely clears deadlock states (pending-upgrade, pending-install, pending-rollback).
It establishes a distributed lease lock via coordination.k8s.io/v1 to prevent split-brain
conditions, enforces stale deployment heuristics, and atomically patches both Kubernetes
Secret metadata labels and internal release JSON.`,
		Args: cobra.ExactArgs(1),
		RunE: runHeal,
	}

	cmd.Flags().StringVar(&healStrategy, "strategy", "mark-failed", "Unlock strategy: 'mark-failed' (recommended for 3-way merge) or 'purge-v1' (revision 1 only)")
	cmd.Flags().BoolVar(&healForce, "force", false, "Force unlock even if the release was modified within the stale threshold")
	cmd.Flags().BoolVar(&healDryRun, "dry-run", false, "Simulate unlock without modifying cluster state")
	cmd.Flags().DurationVar(&healStaleAfter, "stale-after", 10*time.Minute, "Time threshold to verify the release is abandoned before modifying")

	return cmd
}

func runHeal(cmd *cobra.Command, args []string) error {
	releaseName := args[0]
	ctx := context.Background()

	client, defaultNS, err := k8s.NewClient(k8s.ClientConfig{
		KubeconfigPath: kubeconfigPath,
		ContextName:    kubeContext,
	})
	if err != nil {
		return fmt.Errorf("kubernetes client init failed: %w", err)
	}

	targetNS := namespace
	if targetNS == "" {
		targetNS = defaultNS
	}

	strategy := strings.ToLower(strings.TrimSpace(healStrategy))
	if strategy != "mark-failed" && strategy != "purge-v1" {
		return fmt.Errorf("invalid strategy %q: must be 'mark-failed' or 'purge-v1'", healStrategy)
	}

	// 1. Fetch release secret
	detail, err := k8s.GetLatestReleaseSecret(ctx, client, targetNS, releaseName)
	if err != nil {
		return fmt.Errorf("failed to fetch release secret: %w", err)
	}

	// 2. Validate current status
	payloadStatus := ""
	if detail.Payload.Info != nil {
		payloadStatus = detail.Payload.Info.Status
	}

	isStuck := codec.IsPending(detail.Status) || codec.IsPending(payloadStatus)
	if !isStuck {
		fmt.Fprintf(os.Stdout, "Release %q in namespace %q is in state %q (not pending). No deadlock detected. Exiting.\n", releaseName, targetNS, detail.Status)
		return nil
	}

	// 3. Stale Heuristic check
	now := time.Now().UTC()
	var age time.Duration
	if !detail.ModifiedAt.IsZero() {
		age = now.Sub(detail.ModifiedAt)
		if age < healStaleAfter && !healForce {
			return fmt.Errorf("aborting: release %q was modified %s ago (less than stale threshold %s). An active operation might be running. Use --force to override", releaseName, age.Round(time.Second).String(), healStaleAfter.String())
		}
	}

	// 4. Validate strategy for revision
	if strategy == "purge-v1" && detail.Revision != 1 {
		return fmt.Errorf("strategy 'purge-v1' is only valid for revision 1; current revision is v%d (use 'mark-failed')", detail.Revision)
	}

	if healDryRun {
		fmt.Fprintln(os.Stdout, "[DRY-RUN] Execution plan simulated:")
		fmt.Fprintf(os.Stdout, "[DRY-RUN] Target Secret: %s (Namespace: %s)\n", detail.SecretName, targetNS)
		fmt.Fprintf(os.Stdout, "[DRY-RUN] Current Status: %s (Revision: v%d, Age: %s)\n", detail.Status, detail.Revision, age.Round(time.Second).String())
		fmt.Fprintf(os.Stdout, "[DRY-RUN] Strategy: %s\n", strategy)
		if strategy == "purge-v1" {
			fmt.Fprintln(os.Stdout, "[DRY-RUN] Action: DELETE Secret sh.helm.release.v1."+releaseName+".v1")
		} else {
			fmt.Fprintln(os.Stdout, "[DRY-RUN] Action: ATOMIC PATCH Secret labels (status=failed, modifiedAt=now) and .data.release (.info.status=failed)")
		}
		return nil
	}

	// 5. Distributed Mutual Exclusion: acquire Lease lock
	fmt.Fprintf(os.Stdout, "Acquiring distributed lease lock 'helm-lock-%s'...\n", releaseName)
	leaseLock, err := k8s.AcquireLease(ctx, client, targetNS, releaseName, "")
	if err != nil {
		return fmt.Errorf("failed to acquire distributed mutual exclusion lease: %w", err)
	}
	defer func() {
		if relErr := leaseLock.Release(ctx); relErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to cleanly delete lease lock: %v\n", relErr)
		}
	}()
	fmt.Fprintln(os.Stdout, "Distributed lease lock acquired successfully.")

	// 6. Execute Healing
	if strategy == "purge-v1" {
		fmt.Fprintln(os.Stdout, "WARNING: strategy 'purge-v1' completely deletes the revision 1 secret.")
		fmt.Fprintln(os.Stdout, "Any resources created by Helm must be reconciled during subsequent install.")

		if err := k8s.PurgeReleaseSecret(ctx, client, targetNS, detail.SecretName, false); err != nil {
			return fmt.Errorf("failed to purge release secret %s: %w", detail.SecretName, err)
		}

		entries := [][2]string{
			{"Action", "PURGED REVISION 1 SECRET"},
			{"Release", releaseName},
			{"Namespace", targetNS},
			{"Deleted Secret", detail.SecretName},
			{"Next Step", fmt.Sprintf("Run 'helm install %s <chart>' to create a clean revision 1", releaseName)},
		}
		ui.RenderKeyValueBlock(os.Stdout, "Release Unlocked: Purge v1", entries)
		return nil
	}

	// Default strategy: mark-failed
	unlockMsg := "Release unlocked by helm-unwedge"
	updatedSecret, err := k8s.AtomicPatchRelease(ctx, client, detail, codec.StatusFailed, unlockMsg, false)
	if err != nil {
		return fmt.Errorf("failed to execute atomic dual-mutation patch: %w", err)
	}

	entries := [][2]string{
		{"Action", "ATOMIC DUAL-MUTATION UNLOCK"},
		{"Release", releaseName},
		{"Namespace", targetNS},
		{"Revision", fmt.Sprintf("v%d", detail.Revision)},
		{"Secret Patched", updatedSecret.Name},
		{"Previous Status", detail.Status},
		{"New Status", codec.StatusFailed},
		{"Labels Modified", "status=failed, modifiedAt=now"},
		{"Payload Modified", ".info.status=failed, .info.description=" + unlockMsg},
		{"Next Step", fmt.Sprintf("Run 'helm upgrade --install %s <chart>' to perform 3-way merge upgrade", releaseName)},
	}

	ui.RenderKeyValueBlock(os.Stdout, "Release Successfully Unlocked", entries)
	return nil
}
