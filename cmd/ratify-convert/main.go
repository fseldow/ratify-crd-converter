// Command ratify-convert converts Ratify v1 CRDs (config.ratify.deislabs.io/v1beta1)
// into aggregated v2 Executor/NamespacedExecutor resources (config.ratify.sh/v2beta1).
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

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
	)
	cmd := &cobra.Command{
		Use:   "ratify-convert",
		Short: "Convert Ratify v1 CRDs into v2 Executor resources",
		Example: "  ratify-convert -f ./v1-manifests/ -o executor.yaml --scope \"myregistry.io/*\"\n" +
			"  ratify-convert -f store.yaml -f verifier.yaml -f kmp.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(inputs) == 0 {
				return fmt.Errorf("at least one -f/--file input is required")
			}
			rep := report.New()

			bundle, err := loader.LoadPaths(inputs)
			if err != nil {
				return err
			}

			result, err := convert.Aggregate(bundle, convert.Options{
				DefaultScopes: scopes,
				Concurrency:   concurrency,
				Name:          name,
			}, rep)
			if err != nil {
				return err
			}

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

			if diag := rep.String(); diag != "" {
				fmt.Fprint(os.Stderr, "\n--- conversion report ---\n"+diag)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&inputs, "file", "f", nil, "v1 manifest file or directory (repeatable)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file (default: stdout)")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "fallback scopes when none derivable from verifiers")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "Executor concurrency (0 = v2 default)")
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
