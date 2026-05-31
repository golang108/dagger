package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionValues(t *testing.T) {
	scope := artifactListScope{
		Dimensions: []artifactListDimension{
			{Name: "type"},
			{Name: "go-test"},
		},
		Items: []artifactListItem{
			{Coordinates: []*string{ptr("go"), nil}},
			{Coordinates: []*string{ptr("go-test"), ptr("TestFoo")}},
			{Coordinates: []*string{ptr("go-test"), ptr("TestBar")}},
			{Coordinates: []*string{ptr("go-test"), ptr("TestFoo")}},
		},
	}

	values, err := artifactDimensionValues(scope, "types")
	require.NoError(t, err)
	require.Equal(t, []string{"go", "go-test"}, values)

	values, err = artifactDimensionValues(scope, "go-test")
	require.NoError(t, err)
	require.Equal(t, []string{"TestFoo", "TestBar"}, values)
}

func TestArtifactDimensionValuesUnknownDimension(t *testing.T) {
	_, err := artifactDimensionValues(artifactListScope{
		Dimensions: []artifactListDimension{{Name: "type"}},
	}, "go-test")
	require.ErrorContains(t, err, `unknown artifact dimension "go-test" (available: types)`)
}

func TestParseArtifactListArgs(t *testing.T) {
	dimension, filters, help, err := parseArtifactListArgs([]string{
		"go-test",
		"--types=go-test",
		"--go-module", "./app",
		"--go-module=./lib",
	})
	require.NoError(t, err)
	require.False(t, help)
	require.Equal(t, "go-test", dimension)
	require.Equal(t, []artifactListFilter{
		{Dimension: "type", Values: []string{"go-test"}},
		{Dimension: "go-module", Values: []string{"./app", "./lib"}},
	}, filters)
}

func TestParseArtifactListArgsErrors(t *testing.T) {
	_, _, _, err := parseArtifactListArgs([]string{"go-test", "--go-module"})
	require.ErrorContains(t, err, "requires a value")

	_, _, _, err = parseArtifactListArgs([]string{"go-test", "go-module"})
	require.ErrorContains(t, err, "expected exactly one artifact dimension")
}

func TestArtifactListFinalScope(t *testing.T) {
	scope := artifactListFinalScope(artifactListQueryArtifacts{
		Selected: &artifactListQueryArtifacts{
			Dimensions: []artifactListDimension{{Name: "go-test"}},
			Items:      []artifactListItem{{Coordinates: []*string{ptr("TestFoo")}}},
		},
	})
	require.Equal(t, []artifactListDimension{{Name: "go-test"}}, scope.Dimensions)
	require.Len(t, scope.Items, 1)
}

func TestWriteArtifactDimensionValues(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeArtifactDimensionValues(&out, []string{"go", "js"}))
	require.Equal(t, "go\njs\n", out.String())
}

func ptr[T any](v T) *T {
	return &v
}
