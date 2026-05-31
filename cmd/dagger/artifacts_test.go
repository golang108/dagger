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

func TestWriteArtifactDimensionValues(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeArtifactDimensionValues(&out, []string{"go", "js"}))
	require.Equal(t, "go\njs\n", out.String())
}

func ptr[T any](v T) *T {
	return &v
}
