package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var artifactListCmd = &cobra.Command{
	Use:                "list <dimension> [--<dimension>=<value>...]",
	Short:              "List workspace artifact dimension values",
	DisableFlagParsing: true,
	Long: `List values for an artifact dimension.

Use --<dimension> filters to narrow the artifact scope before listing values.
The built-in "types" dimension is an alias for "type".`,
	Example: "dagger list types\n  dagger list go-test --go-module=./app",
	RunE: func(cmd *cobra.Command, args []string) error {
		dimension, filters, help, err := parseArtifactListArgs(args)
		if err != nil {
			return err
		}
		if help {
			return cmd.Help()
		}
		return withEngine(
			cmd.Context(),
			client.Params{LoadWorkspaceModules: true},
			func(ctx context.Context, engineClient *client.Client) error {
				values, err := loadArtifactDimensionValues(ctx, engineClient.Dagger(), dimension, filters)
				if err != nil {
					return err
				}
				return writeArtifactDimensionValues(cmd.OutOrStdout(), values)
			},
		)
	},
}

type artifactListScope struct {
	Dimensions []artifactListDimension `json:"dimensions"`
	Items      []artifactListItem      `json:"items"`
}

type artifactListQueryArtifacts struct {
	Dimensions []artifactListDimension     `json:"dimensions"`
	Items      []artifactListItem          `json:"items"`
	Selected   *artifactListQueryArtifacts `json:"selected"`
}

type artifactListDimension struct {
	Name string `json:"name"`
}

type artifactListItem struct {
	Coordinates []*string `json:"coordinates"`
}

type artifactListFilter struct {
	Dimension string
	Values    []string
}

func parseArtifactListArgs(args []string) (string, []artifactListFilter, bool, error) {
	var dimension string
	filtersByDimension := map[string][]string{}
	filterOrder := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			return "", nil, true, nil
		case arg == "--":
			return "", nil, false, fmt.Errorf("unexpected argument %q", arg)
		case strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--"):
			return "", nil, false, fmt.Errorf("unknown shorthand flag: %q", arg)
		case strings.HasPrefix(arg, "--"):
			name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			if name == "" {
				return "", nil, false, fmt.Errorf("empty artifact filter flag")
			}
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return "", nil, false, fmt.Errorf("artifact filter --%s requires a value", name)
				}
				i++
				value = args[i]
			}
			name = normalizeArtifactDimensionName(name)
			if _, ok := filtersByDimension[name]; !ok {
				filterOrder = append(filterOrder, name)
			}
			filtersByDimension[name] = append(filtersByDimension[name], value)
		default:
			if dimension != "" {
				return "", nil, false, fmt.Errorf("expected exactly one artifact dimension, got %q and %q", dimension, arg)
			}
			dimension = arg
		}
	}
	if dimension == "" {
		return "", nil, false, fmt.Errorf("expected artifact dimension")
	}

	filters := make([]artifactListFilter, 0, len(filterOrder))
	for _, name := range filterOrder {
		filters = append(filters, artifactListFilter{
			Dimension: name,
			Values:    filtersByDimension[name],
		})
	}
	return dimension, filters, false, nil
}

func loadArtifactDimensionValues(
	ctx context.Context,
	dag *dagger.Client,
	dimension string,
	filters []artifactListFilter,
) ([]string, error) {
	scope, err := loadArtifactScope(ctx, dag, filters)
	if err != nil {
		return nil, err
	}
	return artifactDimensionValues(scope, dimension)
}

func loadArtifactScope(ctx context.Context, dag *dagger.Client, filters []artifactListFilter) (artifactListScope, error) {
	const scopeSelection = "dimensions { name } items { coordinates }"

	body := scopeSelection
	varDefParts := make([]string, 0, len(filters))
	vars := map[string]any{}
	for i := len(filters) - 1; i >= 0; i-- {
		varName := fmt.Sprintf("values%d", i)
		varDefParts = append(varDefParts, fmt.Sprintf("$%s: [String!]!", varName))
		vars[varName] = filters[i].Values
		body = fmt.Sprintf(
			"selected: filterCoordinates(dimension: %q, values: $%s) { %s }",
			normalizeArtifactDimensionName(filters[i].Dimension),
			varName,
			body,
		)
	}
	varDefs := ""
	if len(varDefParts) > 0 {
		varDefs = "(" + strings.Join(varDefParts, ", ") + ")"
	}

	query := fmt.Sprintf(`query ArtifactList%s {
  currentWorkspace {
    artifacts {
      %s
    }
  }
}`, varDefs, body)

	var res struct {
		CurrentWorkspace struct {
			Artifacts artifactListQueryArtifacts `json:"artifacts"`
		} `json:"currentWorkspace"`
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     query,
		Variables: vars,
	}, &dagger.Response{Data: &res}); err != nil {
		return artifactListScope{}, err
	}

	return artifactListFinalScope(res.CurrentWorkspace.Artifacts), nil
}

func artifactListFinalScope(artifacts artifactListQueryArtifacts) artifactListScope {
	for artifacts.Selected != nil {
		artifacts = *artifacts.Selected
	}
	return artifactListScope{
		Dimensions: artifacts.Dimensions,
		Items:      artifacts.Items,
	}
}

func artifactDimensionValues(scope artifactListScope, dimension string) ([]string, error) {
	dimName := normalizeArtifactDimensionName(dimension)
	dimIdx := -1
	for i, dim := range scope.Dimensions {
		if dim.Name == dimName {
			dimIdx = i
			break
		}
	}
	if dimIdx == -1 {
		return nil, fmt.Errorf("unknown artifact dimension %q (available: %s)", dimension, availableArtifactDimensions(scope.Dimensions))
	}

	seen := map[string]struct{}{}
	values := make([]string, 0, len(scope.Items))
	for _, item := range scope.Items {
		if dimIdx >= len(item.Coordinates) || item.Coordinates[dimIdx] == nil {
			continue
		}
		value := *item.Coordinates[dimIdx]
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values, nil
}

func normalizeArtifactDimensionName(name string) string {
	if name == "types" {
		return core.ArtifactTypeDimension
	}
	return name
}

func displayArtifactDimensionName(name string) string {
	if name == core.ArtifactTypeDimension {
		return "types"
	}
	return name
}

func availableArtifactDimensions(dimensions []artifactListDimension) string {
	if len(dimensions) == 0 {
		return "none"
	}
	names := make([]string, 0, len(dimensions))
	for _, dim := range dimensions {
		names = append(names, displayArtifactDimensionName(dim.Name))
	}
	return strings.Join(names, ", ")
}

func writeArtifactDimensionValues(w io.Writer, values []string) error {
	for _, value := range values {
		if _, err := fmt.Fprintln(w, value); err != nil {
			return err
		}
	}
	return nil
}
