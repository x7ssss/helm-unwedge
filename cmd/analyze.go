package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/x7ssss/helm-unwedge/pkg/codec"
	"github.com/x7ssss/helm-unwedge/pkg/k8s"
	"github.com/x7ssss/helm-unwedge/pkg/ui"
)

var analyzeStaleAfter time.Duration

func newAnalyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze <release>",
		Short: "Perform read-only diagnostic audit of a Helm release",
		Long: `Analyze inspects the Kubernetes storage secret, metadata labels, internal
payload, and distributed lease locks for a Helm release. It diagnoses whether the
release is deadlocked in a pending state and recommends surgical remedies.`,
		Args: cobra.ExactArgs(1),
		RunE: runAnalyze,
	}

	cmd.Flags().DurationVar(&analyzeStaleAfter, "stale-after", 10*time.Minute, "Staleness threshold before a pending release is deemed abandoned")
	return cmd
}

func runAnalyze(cmd *cobra.Command, args []string) error {
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

	detail, err := k8s.GetLatestReleaseSecret(ctx, client, targetNS, releaseName)
	if err != nil {
		return fmt.Errorf("failed to fetch release secret: %w", err)
	}

	leaseStatus, err := k8s.CheckLease(ctx, client, targetNS, releaseName)
	if err != nil {
		return fmt.Errorf("failed to inspect lease lock: %w", err)
	}

	now := time.Now().UTC()
	var age time.Duration
	ageStr := "unknown"
	if !detail.ModifiedAt.IsZero() {
		age = now.Sub(detail.ModifiedAt).Round(time.Second)
		ageStr = fmt.Sprintf("%s ago (%s)", age.String(), detail.ModifiedAt.Format(time.RFC3339))
	}

	isStuck := codec.IsPending(detail.Status)
	if !isStuck && detail.Payload.Info != nil {
		isStuck = codec.IsPending(detail.Payload.Info.Status)
	}

	isStale := isStuck && age > analyzeStaleAfter

	// Check for label vs payload desync
	payloadStatus := "unknown"
	if detail.Payload.Info != nil {
		payloadStatus = detail.Payload.Info.Status
	}
	desync := detail.Status != payloadStatus

	leaseInfo := "None (Unlocked)"
	if leaseStatus.Exists {
		if leaseStatus.IsHeld {
			leaseInfo = fmt.Sprintf("LOCKED by %s (expires in %s)", leaseStatus.Holder, leaseStatus.ExpiresIn.Round(time.Second))
		} else {
			leaseInfo = fmt.Sprintf("Expired (last held by %s)", leaseStatus.Holder)
		}
	}

	verdict := "HEALTHY"
	recommendation := "No remediation required. Release is in an operable state."

	if isStuck {
		if isStale {
			verdict = "DEADLOCKED (Pending operation abandoned)"
			if detail.Revision == 1 && (detail.Status == codec.StatusPendingInstall || payloadStatus == codec.StatusPendingInstall) {
				recommendation = fmt.Sprintf("Run: helm-unwedge heal %s -n %s --strategy=mark-failed (recommended) OR --strategy=purge-v1", releaseName, targetNS)
			} else {
				recommendation = fmt.Sprintf("Run: helm-unwedge heal %s -n %s", releaseName, targetNS)
			}
		} else {
			verdict = "PENDING (Active run or recent mutation)"
			recommendation = fmt.Sprintf("Release modified %s ago (< %s threshold). Wait for completion or use --force to override.", age.String(), analyzeStaleAfter.String())
		}
	}

	desyncNotice := "Synchronized"
	if desync {
		desyncNotice = fmt.Sprintf("DESYNCHRONIZED (Secret label: %s, Payload JSON: %s)", detail.Status, payloadStatus)
	}

	entries := [][2]string{
		{"Release Name", detail.Name},
		{"Namespace", detail.Namespace},
		{"Latest Revision", fmt.Sprintf("v%d", detail.Revision)},
		{"Secret Name", detail.SecretName},
		{"Secret Label Status", detail.Status},
		{"Payload Status", payloadStatus},
		{"State Alignment", desyncNotice},
		{"Last Modified", ageStr},
		{"Stale Threshold", analyzeStaleAfter.String()},
		{"Distributed Lock", leaseInfo},
		{"Verdict", verdict},
		{"Recommended Action", recommendation},
	}

	ui.RenderKeyValueBlock(os.Stdout, fmt.Sprintf("Helm Release Audit: %s (v%d)", releaseName, detail.Revision), entries)
	return nil
}
