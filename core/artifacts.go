package core

import (
	"fmt"
	"sort"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
)

const ArtifactTypeDimension = "type"

type Verb string

var VerbEnum = dagql.NewEnum[Verb]()

var (
	VerbCheck    = VerbEnum.Register("CHECK", "Validates the artifact.")
	VerbGenerate = VerbEnum.Register("GENERATE", "Produces files in the workspace.")
	VerbShip     = VerbEnum.Register("SHIP", "Publishes the artifact to a remote.")
	VerbUp       = VerbEnum.Register("UP", "Brings up a long-running service.")
)

func (v Verb) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Verb",
		NonNull:   true,
	}
}

func (v Verb) TypeDescription() string {
	return "A standardized artifact lifecycle verb."
}

func (v Verb) Decoder() dagql.InputDecoder {
	return VerbEnum
}

func (v Verb) ToLiteral() call.Literal {
	return VerbEnum.Literal(v)
}

type Artifacts struct {
	Dimensions []*ArtifactDimension `field:"true" doc:"Ordered filterable dimensions for the current scope."`
	rows       []*Artifact
}

func (*Artifacts) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Artifacts",
		NonNull:   true,
	}
}

func (*Artifacts) TypeDescription() string {
	return "A scoped, filterable view over workspace artifacts."
}

type ArtifactDimension struct {
	Name    string   `field:"true" doc:"Filter name as used in CLI flags and table headers."`
	KeyType *TypeDef `field:"true" doc:"Type of this dimension's keys."`
}

func (*ArtifactDimension) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ArtifactDimension",
		NonNull:   true,
	}
}

func (*ArtifactDimension) TypeDescription() string {
	return "A filterable axis of the artifact graph."
}

type Artifact struct {
	coordinates []*string
	scope       *Artifacts
}

func (*Artifact) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Artifact",
		NonNull:   true,
	}
}

func (*Artifact) TypeDescription() string {
	return "One artifact in a workspace artifact scope."
}

func NewWorkspaceArtifacts(mods []dagql.ObjectResult[*Module]) (*Artifacts, error) {
	artifacts := &Artifacts{
		Dimensions: []*ArtifactDimension{
			{
				Name:    ArtifactTypeDimension,
				KeyType: &TypeDef{Kind: TypeDefKindString},
			},
		},
	}

	for _, mod := range mods {
		if mod.Self() == nil {
			continue
		}
		typeDef, ok := mod.Self().mainObjectTypeDefResult()
		if !ok || typeDef.Self() == nil || !typeDef.Self().AsObject.Valid {
			return nil, fmt.Errorf("artifacts from module %q: no main object", mod.Self().Name())
		}
		obj := typeDef.Self().AsObject.Value.Self()
		if obj == nil {
			return nil, fmt.Errorf("artifacts from module %q: no main object", mod.Self().Name())
		}
		artifacts.rows = append(artifacts.rows, &Artifact{
			coordinates: []*string{ptr(strcase.ToKebab(obj.Name))},
		})
	}

	sort.SliceStable(artifacts.rows, func(i, j int) bool {
		return *artifacts.rows[i].coordinates[0] < *artifacts.rows[j].coordinates[0]
	})

	return artifacts, nil
}

func (a *Artifacts) dimensionIndex(name string) (int, bool) {
	for i, dim := range a.Dimensions {
		if dim != nil && dim.Name == name {
			return i, true
		}
	}
	return -1, false
}

func (a *Artifacts) FilterDimension(dimension string) (*Artifacts, error) {
	idx, ok := a.dimensionIndex(dimension)
	if !ok {
		return nil, fmt.Errorf("artifact dimension %q is not present in this scope", dimension)
	}

	filtered := &Artifacts{Dimensions: a.Dimensions}
	for _, row := range a.rows {
		if idx < len(row.coordinates) && row.coordinates[idx] != nil {
			filtered.rows = append(filtered.rows, row)
		}
	}
	return filtered, nil
}

func (a *Artifacts) FilterCoordinates(dimension string, values []string) (*Artifacts, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("artifact coordinate filter for %q requires at least one value", dimension)
	}

	idx, ok := a.dimensionIndex(dimension)
	if !ok {
		return nil, fmt.Errorf("artifact dimension %q is not present in this scope", dimension)
	}

	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}

	filtered := &Artifacts{Dimensions: a.Dimensions}
	for _, row := range a.rows {
		if idx >= len(row.coordinates) || row.coordinates[idx] == nil {
			continue
		}
		if _, ok := allowed[*row.coordinates[idx]]; ok {
			filtered.rows = append(filtered.rows, row)
		}
	}
	return filtered, nil
}

func (a *Artifacts) Items() []*Artifact {
	items := make([]*Artifact, len(a.rows))
	for i, row := range a.rows {
		items[i] = &Artifact{
			coordinates: row.coordinates,
			scope:       a,
		}
	}
	return items
}

func (a *Artifact) Coordinates() []*string {
	if a == nil {
		return nil
	}
	coordinates := make([]*string, len(a.coordinates))
	for i, coord := range a.coordinates {
		if coord != nil {
			coordinates[i] = ptr(*coord)
		}
	}
	return coordinates
}

func (a *Artifact) Scope() *Artifacts {
	if a == nil {
		return nil
	}
	return a.scope
}

func (a *Artifact) Coordinate(name string) (string, bool) {
	if a == nil || a.scope == nil {
		return "", false
	}
	idx, ok := a.scope.dimensionIndex(name)
	if !ok || idx >= len(a.coordinates) || a.coordinates[idx] == nil {
		return "", false
	}
	return *a.coordinates[idx], true
}
