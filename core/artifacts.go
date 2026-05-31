package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"

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
	coordinates         []*string
	selectors           []dagql.Selector
	collectionSelectors []dagql.Selector
	scope               *Artifacts
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

func NewWorkspaceArtifacts(ctx context.Context, dag *dagql.Server, mods []dagql.ObjectResult[*Module]) (*Artifacts, error) {
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
		for _, def := range mod.Self().ObjectDefs {
			typeDef := def.Self()
			if typeDef == nil || !typeDef.AsCollection.Valid {
				continue
			}
			collection := typeDef.AsCollection.Value.Self()
			if collection == nil || !collection.KeyType.Valid || !collection.ValueType.Valid {
				continue
			}
			dimName, ok := collectionItemDimension(collection)
			if !ok {
				continue
			}
			artifacts.ensureDimension(dimName, collection.KeyType.Value.Self())
		}
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
		rootSelector := dagql.Selector{Field: gqlFieldName(mod.Self().Name())}
		artifacts.addArtifactRow(strcase.ToKebab(obj.Name), nil, []dagql.Selector{rootSelector}, nil)
		if dag != nil {
			if err := artifacts.addCollectionRows(ctx, dag, mod, obj); err != nil {
				return nil, err
			}
		}
	}

	sort.SliceStable(artifacts.rows, func(i, j int) bool { return artifactRowLess(artifacts.rows[i], artifacts.rows[j]) })

	return artifacts, nil
}

func (a *Artifacts) ensureDimension(name string, keyType *TypeDef) int {
	if idx, ok := a.dimensionIndex(name); ok {
		return idx
	}
	a.Dimensions = append(a.Dimensions, &ArtifactDimension{
		Name:    name,
		KeyType: keyType,
	})
	return len(a.Dimensions) - 1
}

func (a *Artifacts) addCollectionRows(
	ctx context.Context,
	dag *dagql.Server,
	mod dagql.ObjectResult[*Module],
	mainObj *ObjectTypeDef,
) error {
	if mainObj.Constructor.Valid && functionRequiresArgs(mainObj.Constructor.Value.Self()) {
		return nil
	}

	rootSelector := dagql.Selector{Field: gqlFieldName(mod.Self().Name())}
	seen := map[string]struct{}{mainObj.Name: {}}
	return a.addCollectionRowsFromObject(ctx, dag, mod.Self(), mainObj, []dagql.Selector{rootSelector}, nil, seen)
}

func (a *Artifacts) addCollectionRowsFromObject(
	ctx context.Context,
	dag *dagql.Server,
	mod *Module,
	obj *ObjectTypeDef,
	selectors []dagql.Selector,
	coordinates map[string]string,
	seen map[string]struct{},
) error {
	for _, field := range obj.Fields {
		fieldSelf := field.Self()
		if fieldSelf == nil {
			continue
		}
		fieldSelector := dagql.Selector{Field: fieldSelf.Name}
		if collection, ok := mod.collectionForTypeDef(fieldSelf.TypeDef.Self()); ok {
			if err := a.addCollectionRowsForSelector(ctx, dag, mod, appendArtifactSelector(selectors, fieldSelector), collection, coordinates, seen); err != nil {
				return fmt.Errorf("artifact collection %s.%s: %w", obj.Name, fieldSelf.Name, err)
			}
			continue
		}
		if err := a.addCollectionRowsFromMember(ctx, dag, mod, fieldSelf.TypeDef.Self(), appendArtifactSelector(selectors, fieldSelector), coordinates, seen); err != nil {
			return fmt.Errorf("artifact structural field %s.%s: %w", obj.Name, fieldSelf.Name, err)
		}
	}
	for _, fn := range obj.Functions {
		fnSelf := fn.Self()
		if fnSelf == nil || functionRequiresArgs(fnSelf) {
			continue
		}
		fnSelector := dagql.Selector{Field: fnSelf.Name}
		if collection, ok := mod.collectionForTypeDef(fnSelf.ReturnType.Self()); ok {
			if err := a.addCollectionRowsForSelector(ctx, dag, mod, appendArtifactSelector(selectors, fnSelector), collection, coordinates, seen); err != nil {
				return fmt.Errorf("artifact collection %s.%s: %w", obj.Name, fnSelf.Name, err)
			}
			continue
		}
		if err := a.addCollectionRowsFromMember(ctx, dag, mod, fnSelf.ReturnType.Self(), appendArtifactSelector(selectors, fnSelector), coordinates, seen); err != nil {
			return fmt.Errorf("artifact structural function %s.%s: %w", obj.Name, fnSelf.Name, err)
		}
	}
	return nil
}

func (a *Artifacts) addCollectionRowsFromMember(
	ctx context.Context,
	dag *dagql.Server,
	mod *Module,
	typeDef *TypeDef,
	selectors []dagql.Selector,
	coordinates map[string]string,
	seen map[string]struct{},
) error {
	obj, ok := mod.objectForTypeDef(typeDef)
	if !ok {
		return nil
	}
	if _, ok := seen[obj.Name]; ok {
		return nil
	}
	nextSeen := maps.Clone(seen)
	nextSeen[obj.Name] = struct{}{}
	return a.addCollectionRowsFromObject(ctx, dag, mod, obj, selectors, coordinates, nextSeen)
}

func (mod *Module) collectionForTypeDef(typeDef *TypeDef) (*CollectionTypeDef, bool) {
	def, obj, ok := mod.objectTypeDefForTypeDef(typeDef)
	if !ok || !def.Self().AsCollection.Valid {
		return nil, false
	}
	return def.Self().AsCollection.Value.Self(), obj != nil
}

func (mod *Module) objectForTypeDef(typeDef *TypeDef) (*ObjectTypeDef, bool) {
	_, obj, ok := mod.objectTypeDefForTypeDef(typeDef)
	return obj, ok
}

func (mod *Module) objectTypeDefForTypeDef(typeDef *TypeDef) (dagql.ObjectResult[*TypeDef], *ObjectTypeDef, bool) {
	if typeDef == nil || typeDef.Kind != TypeDefKindObject || !typeDef.AsObject.Valid || typeDef.AsObject.Value.Self() == nil {
		return dagql.ObjectResult[*TypeDef]{}, nil, false
	}
	for _, def := range mod.ObjectDefs {
		defSelf := def.Self()
		if defSelf == nil || !defSelf.AsObject.Valid || defSelf.AsObject.Value.Self() == nil {
			continue
		}
		if defSelf.AsObject.Value.Self().Name == typeDef.AsObject.Value.Self().Name {
			return def, defSelf.AsObject.Value.Self(), true
		}
	}
	return dagql.ObjectResult[*TypeDef]{}, nil, false
}

func (a *Artifacts) addCollectionRowsForSelector(
	ctx context.Context,
	dag *dagql.Server,
	mod *Module,
	collectionSelectors []dagql.Selector,
	collection *CollectionTypeDef,
	coordinates map[string]string,
	seen map[string]struct{},
) error {
	dimName, ok := collectionItemDimension(collection)
	if !ok {
		return nil
	}
	if _, ok := a.dimensionIndex(dimName); !ok {
		return nil
	}
	var keys dagql.AnyResult
	keySelectors := appendArtifactSelector(collectionSelectors, dagql.Selector{Field: collectionKeysFieldName})
	if err := dag.Select(ctx, dag.Root(), &keys, keySelectors...); err != nil {
		return err
	}
	values, err := artifactCollectionKeys(keys)
	if err != nil {
		return err
	}
	for _, value := range values {
		nextCoordinates := maps.Clone(coordinates)
		if nextCoordinates == nil {
			nextCoordinates = map[string]string{}
		}
		getSelector := dagql.Selector{
			Field: collectionGetFunctionName,
			Args:  []dagql.NamedInput{{Name: collectionGetArgName, Value: value.input}},
		}
		itemSelectors := appendArtifactSelector(collectionSelectors, getSelector)
		nextCoordinates[dimName] = value.coordinate
		a.addArtifactRow(dimName, nextCoordinates, itemSelectors, collectionSelectors)

		valueType := collection.ValueType.Value.Self()
		valueObj, ok := mod.objectForTypeDef(valueType)
		if !ok {
			continue
		}
		if _, ok := seen[valueObj.Name]; ok {
			continue
		}
		nextSeen := maps.Clone(seen)
		nextSeen[valueObj.Name] = struct{}{}
		if err := a.addCollectionRowsFromObject(ctx, dag, mod, valueObj, itemSelectors, nextCoordinates, nextSeen); err != nil {
			return fmt.Errorf("artifact collection item %s=%s: %w", dimName, value.coordinate, err)
		}
	}
	return nil
}

func (a *Artifacts) addArtifactRow(
	artifactType string,
	coordinates map[string]string,
	selectors []dagql.Selector,
	collectionSelectors []dagql.Selector,
) {
	coords := make([]*string, len(a.Dimensions))
	coords[0] = ptr(artifactType)
	for dimension, value := range coordinates {
		if dimIdx, ok := a.dimensionIndex(dimension); ok {
			coords[dimIdx] = ptr(value)
		}
	}
	a.rows = append(a.rows, &Artifact{
		coordinates:         coords,
		selectors:           cloneArtifactSelectors(selectors),
		collectionSelectors: cloneArtifactSelectors(collectionSelectors),
	})
}

func collectionItemDimension(collection *CollectionTypeDef) (string, bool) {
	if collection == nil || !collection.ValueType.Valid {
		return "", false
	}
	valueType := collection.ValueType.Value.Self()
	if valueType == nil || valueType.Kind != TypeDefKindObject || !valueType.AsObject.Valid || valueType.AsObject.Value.Self() == nil {
		return "", false
	}
	return strcase.ToKebab(valueType.AsObject.Value.Self().Name), true
}

type artifactCollectionKey struct {
	input      dagql.Input
	coordinate string
}

func artifactCollectionKeys(keys dagql.AnyResult) ([]artifactCollectionKey, error) {
	list, ok := dagql.UnwrapAs[dagql.Enumerable](keys)
	if !ok {
		return nil, fmt.Errorf("collection keys resolved to %T, not a list", keys)
	}
	values := make([]artifactCollectionKey, 0, list.Len())
	for i := 1; i <= list.Len(); i++ {
		item, err := list.Nth(i)
		if err != nil {
			return nil, err
		}
		input, ok := item.(dagql.Input)
		if !ok {
			return nil, fmt.Errorf("collection key resolved to %T, not an input", item)
		}
		value, err := artifactCollectionKeyValue(item)
		if err != nil {
			return nil, err
		}
		values = append(values, artifactCollectionKey{
			input:      input,
			coordinate: value,
		})
	}
	return values, nil
}

func appendArtifactSelector(selectors []dagql.Selector, selector dagql.Selector) []dagql.Selector {
	next := make([]dagql.Selector, 0, len(selectors)+1)
	next = append(next, selectors...)
	next = append(next, selector)
	return next
}

func cloneArtifactSelectors(selectors []dagql.Selector) []dagql.Selector {
	if len(selectors) == 0 {
		return nil
	}
	cloned := make([]dagql.Selector, len(selectors))
	for i, selector := range selectors {
		cloned[i] = selector
		cloned[i].Args = append([]dagql.NamedInput(nil), selector.Args...)
	}
	return cloned
}

func artifactCollectionKeyValue(value dagql.Typed) (string, error) {
	switch value := value.(type) {
	case dagql.String:
		return value.String(), nil
	case dagql.Int:
		return strconv.FormatInt(value.Int64(), 10), nil
	case dagql.Float:
		return strconv.FormatFloat(float64(value), 'f', -1, 64), nil
	case dagql.Boolean:
		return strconv.FormatBool(bool(value)), nil
	case *dagql.EnumValueName:
		return value.Name, nil
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(payload), nil
	}
}

func artifactRowLess(left, right *Artifact) bool {
	maxLen := len(left.coordinates)
	if len(right.coordinates) > maxLen {
		maxLen = len(right.coordinates)
	}
	for i := 0; i < maxLen; i++ {
		var leftVal, rightVal string
		if i < len(left.coordinates) && left.coordinates[i] != nil {
			leftVal = *left.coordinates[i]
		}
		if i < len(right.coordinates) && right.coordinates[i] != nil {
			rightVal = *right.coordinates[i]
		}
		if leftVal != rightVal {
			return leftVal < rightVal
		}
	}
	return false
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
			coordinates:         row.coordinates,
			selectors:           row.selectors,
			collectionSelectors: row.collectionSelectors,
			scope:               a,
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

func (a *Artifact) Selectors() []dagql.Selector {
	if a == nil {
		return nil
	}
	return cloneArtifactSelectors(a.selectors)
}

func (a *Artifact) CollectionSelectors() []dagql.Selector {
	if a == nil {
		return nil
	}
	return cloneArtifactSelectors(a.collectionSelectors)
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
