// Command ratify-convert converts Ratify v1 CRDs (config.ratify.deislabs.io/v1beta1)
// into aggregated v2 Executor/NamespacedExecutor resources (config.ratify.sh/v2beta1).
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/fseldow/ratify-crd-converter/internal/cluster"
	"github.com/fseldow/ratify-crd-converter/internal/convert"
	"github.com/fseldow/ratify-crd-converter/internal/loader"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		inputs      []string
		output      string
		scopes      []string
		concurrency int
		name        string
		fromCluster bool
		applyToCl   bool
		kubeconfig  string
		dryRun      bool
	)
	cmd := &cobra.Command{
		Use:   "ratify-convert",
		Short: "Convert Ratify v1 CRDs into v2 Executor resources",
		Example: "  # From files to stdout\n" +
			"  ratify-convert -f ./v1-manifests/ -o executor.yaml --scope \"myregistry.io/*\"\n\n" +
			"  # Read all v1 CRs from the current cluster and apply the migrated v2 in place\n" +
			"  ratify-convert --from-cluster --apply\n\n" +
			"  # Read from cluster, preview the v2 YAML without applying\n" +
			"  ratify-convert --from-cluster -o -",
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromCluster && len(inputs) > 0 {
				return fmt.Errorf("use either --from-cluster or -f/--file, not both")
			}
			if !fromCluster && len(inputs) == 0 {
				return fmt.Errorf("provide -f/--file inputs or --from-cluster")
			}
			rep := report.New()

			var (
				bundle *loader.Bundle
				cl     *cluster.Client
				err    error
			)
			if fromCluster {
				cl, err = cluster.New(kubeconfig)
				if err != nil {
					return err
				}
				bundle, err = cl.Load(context.Background())
				if err != nil {
					return err
				}
			} else {
				bundle, err = loader.LoadPaths(inputs)
				if err != nil {
					return err
				}
			}

			result, err := convert.Aggregate(bundle, convert.Options{
				DefaultScopes: scopes,
				Concurrency:   concurrency,
				Name:          name,
			}, rep)
			if err != nil {
				return err
			}

			if applyToCl {
				if cl == nil {
					cl, err = cluster.New(kubeconfig)
					if err != nil {
						return err
					}
				}
				if err := cl.Apply(context.Background(), result.Executors, result.NamespacedExecutors, dryRun); err != nil {
					return err
				}
				action := "applied"
				if dryRun {
					action = "validated (server dry-run)"
				}
				fmt.Fprintf(os.Stderr, "%s %d Executor(s) and %d NamespacedExecutor(s) to the cluster\n",
					action, len(result.Executors), len(result.NamespacedExecutors))
			}

			// Emit YAML unless we applied and no explicit output was requested.
			if !applyToCl || output != "" {
				out, err := render(result)
				if err != nil {
					return err
				}
				if output == "" || output == "-" {
					fmt.Print(string(out))
				} else if err := os.WriteFile(output, out, 0o644); err != nil {
					return err
				} else {
					fmt.Fprintf(os.Stderr, "wrote %s\n", output)
				}
			}

			if diag := rep.String(); diag != "" {
				fmt.Fprint(os.Stderr, "\n--- conversion report ---\n"+diag)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&inputs, "file", "f", nil, "v1 manifest file or directory (repeatable)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file, or - for stdout (default: stdout unless --apply)")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "fallback scopes when none derivable from verifiers")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "Executor concurrency (0 = v2 default)")
	cmd.Flags().BoolVarP(&fromCluster, "from-cluster", "k", false, "read all v1 CRs directly from the cluster instead of files")
	cmd.Flags().BoolVar(&applyToCl, "apply", false, "apply the generated v2 Executor(s) to the cluster (server-side apply)")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (default: $KUBECONFIG or ~/.kube/config)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --apply, use server-side dry-run (nothing persisted)")
	cmd.Flags().StringVar(&name, "name", "executor", "metadata.name for generated executors")
	return cmd
}

// render serializes all executors into a single multi-document YAML byte slice.
func render(r *convert.Result) ([]byte, error) {
	var buf bytes.Buffer
	first := true
	emit := func(v any) error {
		b, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		if !first {
			buf.WriteString("---\n")
		}
		first = false
		buf.Write(b)
		return nil
	}
	for _, e := range r.Executors {
		if err := emit(e); err != nil {
			return nil, err
		}
	}
	for _, e := range r.NamespacedExecutors {
		if err := emit(e); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
