package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	kubeconfigPath string
	kubeContext    string
	namespace      string

	rootCmd = &cobra.Command{
		Use:   "helm-unwedge",
		Short: "Zero-dependency CLI for diagnosing and unlocking deadlocked Helm v3 releases",
		Long: `helm-unwedge is a surgical, zero-dependency tool designed to diagnose and
unlock deadlocked Helm v3 releases (pending-upgrade, pending-install, pending-rollback)
without importing the Helm SDK. It performs atomic dual-mutations on Kubernetes secrets
and coordinates distributed locks using coordination.k8s.io Leases.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&kubeconfigPath, "kubeconfig", "k", "", "Path to the kubeconfig file")
	rootCmd.PersistentFlags().StringVarP(&kubeContext, "context", "c", "", "Kubernetes context to use")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace scope")

	rootCmd.AddCommand(newAnalyzeCmd())
	rootCmd.AddCommand(newHealCmd())
	rootCmd.AddCommand(newAutoCmd())
	rootCmd.AddCommand(newListStuckCmd())
}

// Execute runs the root CLI command.
func Execute() error {
	return rootCmd.Execute()
}

// MainEntryPoint is invoked by main.go.
func MainEntryPoint() {
	if err := Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
