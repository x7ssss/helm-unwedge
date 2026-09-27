package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/x7ssss/helm-unwedge/pkg/codec"
	"github.com/x7ssss/helm-unwedge/pkg/k8s"
)

var (
	autoReleaseName string
	autoNamespace   string
	autoStaleAfter  time.Duration
)

func newAutoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto",
		Short: "Zero-config CI gatekeeper for unlocking deadlocked releases",
		Long: `Auto evaluates a Helm release before deployment steps in CI pipelines.
If the release is healthy, it exits 0 immediately.
If an active concurrent deployment is underway (within the stale threshold or lease locked), it exits 1.
If the release is abandoned and deadlocked in a pending state, it acquires a lease lock,
heals the release to 'failed' status, and exits 0 so the subsequent Helm step can proceed.`,
		RunE: runAuto,
	}

	cmd.Flags().StringVarP(&autoReleaseName, "release", "r", "", "Name of the Helm release (required)")
	cmd.Flags().StringVarP(&autoNamespace, "namespace", "n", "", "Namespace of the Helm release (required)")
	cmd.Flags().DurationVar(&autoStaleAfter, "stale-after", 10*time.Minute, "Staleness threshold before pending release is treated as deadlocked")

	_ = cmd.MarkFlagRequired("release")

	return cmd
}

func runAuto(cmd *cobra.Command, args []string) error {
	if autoReleaseName == "" {
		return errors.New("--release flag is required")
	}

	ctx := context.Background()

	client, defaultNS, err := k8s.NewClient(k8s.ClientConfig{
		KubeconfigPath: kubeconfigPath,
		ContextName:    kubeContext,
	})
	if err != nil {
		return fmt.Errorf("kubernetes client init failed: %w", err)
	}

	targetNS := autoNamespace
	if targetNS == "" {
		targetNS = namespace
	}
	if targetNS == "" {
		targetNS = defaultNS
	}

	// 1. Fetch release secret
	detail, err := k8s.GetLatestReleaseSecret(ctx, client, targetNS, autoReleaseName)
	if err != nil {
		// If secret doesn't exist, release hasn't been installed yet. CI can proceed.
		fmt.Fprintf(os.Stdout, "Notice: no existing release secrets found for %q in %q. Ready for initial installation. Exiting 0.\n", autoReleaseName, targetNS)
		return nil
	}

	// 2. Inspect state
	payloadStatus := ""
	if detail.Payload.Info != nil {
		payloadStatus = detail.Payload.Info.Status
	}

	isStuck := codec.IsPending(detail.Status) || codec.IsPending(payloadStatus)
	if !isStuck {
		fmt.Fprintf(os.Stdout, "Release %q in %q is in healthy state %q (v%d). Exiting 0.\n", autoReleaseName, targetNS, detail.Status, detail.Revision)
		return nil
	}

	// 3. Check for active Lease Lock
	leaseStatus, err := k8s.CheckLease(ctx, client, targetNS, autoReleaseName)
	if err != nil {
		return fmt.Errorf("failed to check lease status: %w", err)
	}
	if leaseStatus.Exists && leaseStatus.IsHeld {
		fmt.Fprintf(os.Stderr, "Concurrent execution detected: release %q is locked by %s (lease expires in %s). Exiting 1.\n", autoReleaseName, leaseStatus.Holder, leaseStatus.ExpiresIn.Round(time.Second))
		os.Exit(1)
	}

	// 4. Stale Heuristic
	now := time.Now().UTC()
	var age time.Duration
	if !detail.ModifiedAt.IsZero() {
		age = now.Sub(detail.ModifiedAt)
		if age < autoStaleAfter {
			fmt.Fprintf(os.Stderr, "Concurrent execution detected: release %q was modified %s ago (< %s threshold). Active deployment likely. Exiting 1.\n", autoReleaseName, age.Round(time.Second).String(), autoStaleAfter.String())
			os.Exit(1)
		}
	}

	// 5. Release is deadlocked and stale: acquire lease and heal
	fmt.Fprintf(os.Stdout, "Deadlock detected: release %q is stuck in %q for %s (> %s threshold). Initiating automated unlock...\n", autoReleaseName, detail.Status, age.Round(time.Second).String(), autoStaleAfter.String())

	leaseLock, err := k8s.AcquireLease(ctx, client, targetNS, autoReleaseName, "ci-auto-unlocker")
	if err != nil {
		if errors.Is(err, k8s.ErrLockActive) {
			fmt.Fprintf(os.Stderr, "Concurrent execution detected while acquiring lease: %v. Exiting 1.\n", err)
			os.Exit(1)
		}
		return fmt.Errorf("failed to acquire lease for auto heal: %w", err)
	}
	defer func() {
		_ = leaseLock.Release(ctx)
	}()

	unlockMsg := "Auto-unlocked deadlocked release in CI pipeline"
	_, err = k8s.AtomicPatchRelease(ctx, client, detail, codec.StatusFailed, unlockMsg, false)
	if err != nil {
		return fmt.Errorf("failed to auto-heal deadlocked release %q: %w", autoReleaseName, err)
	}

	fmt.Fprintf(os.Stdout, "Successfully unlocked deadlocked release %q (v%d). Status reset to 'failed'. CI pipeline can proceed. Exiting 0.\n", autoReleaseName, detail.Revision)
	return nil
}
