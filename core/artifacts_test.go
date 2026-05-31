package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

func TestArtifactsFilterCoordinates(t *testing.T) {
	artifacts := &Artifacts{
		Dimensions: []*ArtifactDimension{
			{Name: ArtifactTypeDimension, KeyType: &TypeDef{Kind: TypeDefKindString}},
		},
		rows: []*Artifact{
			{coordinates: []*string{ptr("go")}},
			{coordinates: []*string{ptr("js")}},
			{coordinates: []*string{ptr("go-test")}},
		},
	}

	filtered, err := artifacts.FilterCoordinates(ArtifactTypeDimension, []string{"go", "js"})
	require.NoError(t, err)
	items := filtered.Items()
	require.Len(t, items, 2)
	value, ok := items[0].Coordinate(ArtifactTypeDimension)
	require.True(t, ok)
	require.Equal(t, "go", value)
	value, ok = items[1].Coordinate(ArtifactTypeDimension)
	require.True(t, ok)
	require.Equal(t, "js", value)
}

func TestArtifactsFilterDimension(t *testing.T) {
	artifacts := &Artifacts{
		Dimensions: []*ArtifactDimension{
			{Name: ArtifactTypeDimension, KeyType: &TypeDef{Kind: TypeDefKindString}},
			{Name: "go-test", KeyType: &TypeDef{Kind: TypeDefKindString}},
		},
		rows: []*Artifact{
			{coordinates: []*string{ptr("go"), nil}},
			{coordinates: []*string{ptr("go-test"), ptr("TestFoo")}},
		},
	}

	filtered, err := artifacts.FilterDimension("go-test")
	require.NoError(t, err)
	items := filtered.Items()
	require.Len(t, items, 1)
	value, ok := items[0].Coordinate("go-test")
	require.True(t, ok)
	require.Equal(t, "TestFoo", value)
}

func TestArtifactsFiltersComposeAndPreserveDimensions(t *testing.T) {
	artifacts := &Artifacts{
		Dimensions: []*ArtifactDimension{
			{Name: ArtifactTypeDimension, KeyType: &TypeDef{Kind: TypeDefKindString}},
			{Name: "go-test", KeyType: &TypeDef{Kind: TypeDefKindString}},
		},
		rows: []*Artifact{
			{coordinates: []*string{ptr("go"), nil}},
			{coordinates: []*string{ptr("go-test"), ptr("TestFoo")}},
			{coordinates: []*string{ptr("go-test"), ptr("TestBar")}},
			{coordinates: []*string{ptr("js"), nil}},
		},
	}

	filtered, err := artifacts.FilterCoordinates(ArtifactTypeDimension, []string{"go-test", "js"})
	require.NoError(t, err)
	filtered, err = filtered.FilterCoordinates("go-test", []string{"TestFoo"})
	require.NoError(t, err)

	require.Equal(t, []string{ArtifactTypeDimension, "go-test"}, artifactDimensionNames(filtered.Dimensions))
	items := filtered.Items()
	require.Len(t, items, 1)
	value, ok := items[0].Coordinate(ArtifactTypeDimension)
	require.True(t, ok)
	require.Equal(t, "go-test", value)
	value, ok = items[0].Coordinate("go-test")
	require.True(t, ok)
	require.Equal(t, "TestFoo", value)
}

func TestArtifactCoordinatesAreReadOnlyProjection(t *testing.T) {
	artifacts := &Artifacts{
		Dimensions: []*ArtifactDimension{
			{Name: ArtifactTypeDimension, KeyType: &TypeDef{Kind: TypeDefKindString}},
		},
		rows: []*Artifact{
			{
				coordinates: []*string{ptr("go")},
				selectors: []dagql.Selector{
					{
						Field: "go",
						Args:  []dagql.NamedInput{{Name: "key", Value: dagql.String("unit")}},
					},
				},
				collectionSelectors: []dagql.Selector{{Field: "tests"}},
			},
		},
	}

	item := artifacts.Items()[0]
	coords := item.Coordinates()
	*coords[0] = "js"
	selectors := item.Selectors()
	selectors[0].Field = "changed"
	selectors[0].Args[0].Name = "changed"
	collectionSelectors := item.CollectionSelectors()
	collectionSelectors[0].Field = "changed"

	value, ok := item.Coordinate(ArtifactTypeDimension)
	require.True(t, ok)
	require.Equal(t, "go", value)
	require.Equal(t, "go", item.Selectors()[0].Field)
	require.Equal(t, "key", item.Selectors()[0].Args[0].Name)
	require.Equal(t, "tests", item.CollectionSelectors()[0].Field)
	require.Same(t, artifacts, item.Scope())
}

func TestArtifactsFilterErrors(t *testing.T) {
	artifacts := &Artifacts{
		Dimensions: []*ArtifactDimension{{Name: ArtifactTypeDimension}},
	}

	_, err := artifacts.FilterCoordinates("missing", []string{"go"})
	require.ErrorContains(t, err, `artifact dimension "missing" is not present`)

	_, err = artifacts.FilterCoordinates(ArtifactTypeDimension, nil)
	require.ErrorContains(t, err, "requires at least one value")

	_, err = artifacts.FilterDimension("missing")
	require.ErrorContains(t, err, `artifact dimension "missing" is not present`)
}

func artifactDimensionNames(dimensions []*ArtifactDimension) []string {
	names := make([]string, 0, len(dimensions))
	for _, dimension := range dimensions {
		names = append(names, dimension.Name)
	}
	return names
}
