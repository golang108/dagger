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

var artifactListTypeFilters []string

var artifactListCmd = &cobra.Command{
	Use:   "list <dimension>",
	Short: "List workspace artifact dimension values",
	Long: `List values for an artifact dimension.

Available dimensions:
  types  List available artifact types`,
	Example: "dagger list types",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(
			cmd.Context(),
			client.Params{LoadWorkspaceModules: true},
			func(ctx context.Context, engineClient *client.Client) error {
				values, err := loadArtifactDimensionValues(ctx, engineClient.Dagger(), args[0], artifactListTypeFilters)
				if err != nil {
					return err
				}
				return writeArtifactDimensionValues(cmd.OutOrStdout(), values)
			},
		)
	},
}

func init() {
	artifactListCmd.Flags().StringArrayVar(&artifactListTypeFilters, core.ArtifactTypeDimension, nil, "Filter by artifact type")
}

type artifactListScope struct {
	Dimensions []artifactListDimension `json:"dimensions"`
	Items      []artifactListItem      `json:"items"`
}

type artifactListQueryArtifacts struct {
	Dimensions []artifactListDimension `json:"dimensions"`
	Items      []artifactListItem      `json:"items"`
	Selected   *artifactListScope      `json:"selected"`
}

type artifactListDimension struct {
	Name string `json:"name"`
}

type artifactListItem struct {
	Coordinates []*string `json:"coordinates"`
}

func loadArtifactDimensionValues(
	ctx context.Context,
	dag *dagger.Client,
	dimension string,
	typeFilters []string,
) ([]string, error) {
	scope, err := loadArtifactScope(ctx, dag, typeFilters)
	if err != nil {
		return nil, err
	}
	return artifactDimensionValues(scope, dimension)
}

func loadArtifactScope(ctx context.Context, dag *dagger.Client, typeFilters []string) (artifactListScope, error) {
	const scopeSelection = "dimensions { name } items { coordinates }"

	body := scopeSelection
	varDefs := ""
	vars := map[string]any{}
	if len(typeFilters) > 0 {
		varDefs = "($type: [String!]!)"
		vars["type"] = typeFilters
		body = fmt.Sprintf("selected: filterCoordinates(dimension: %q, values: $type) { %s }", core.ArtifactTypeDimension, scopeSelection)
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

	artifacts := res.CurrentWorkspace.Artifacts
	if artifacts.Selected != nil {
		return *artifacts.Selected, nil
	}
	return artifactListScope{
		Dimensions: artifacts.Dimensions,
		Items:      artifacts.Items,
	}, nil
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
