package pluginbinding

import (
	datasource "github.com/fluxplane/fluxplane-datasource"
	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

const (
	DatasourceViewCompact = datasource.DeclarationViewCompact
	DatasourceViewDetail  = datasource.DeclarationViewDetail
	DatasourceViewLookup  = datasource.DeclarationViewLookup
	DatasourceViewTable   = datasource.DeclarationViewTable
)

func EntitySchema(schema manifest.DatasourceEntitySchema) DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		copied := schema
		*spec = datasource.NormalizeDeclaration(datasource.MergeDeclarationSchema(*spec, copied))
	}
}

func EntitySchemaFor[T any]() DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		generated := datasource.EntitySchemaFor[T]()
		if spec.EntitySchema != nil {
			generated = datasource.MergeEntitySchema(generated, *spec.EntitySchema)
		}
		spec.EntitySchema = &generated
	}
}

func View(name, description string, fields ...string) DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		spec.Views = append(spec.Views, manifest.DatasourceViewSpec{Name: name, Description: description, Fields: append([]string(nil), fields...)})
	}
}

func Relation(name, field, entity, relationType string) DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		spec.Relations = append(spec.Relations, manifest.DatasourceRelationSpec{Name: name, Field: field, Entity: entity, Type: relationType})
	}
}

func Fallback(fallback manifest.DatasourceFallback) DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		spec.Fallback = fallback
	}
}

func Completion(description string, fields ...string) DatasourceSpecOption {
	return func(spec *manifest.DatasourceSpec) {
		spec.Completion = &manifest.DatasourceCompletionSpec{Description: description, Fields: append([]string(nil), fields...)}
	}
}

func normalizeDatasourceSpecs(specs []manifest.DatasourceSpec) []manifest.DatasourceSpec {
	return datasource.NormalizeDeclarations(specs)
}

func NormalizeDatasourceSpec(spec manifest.DatasourceSpec) manifest.DatasourceSpec {
	return datasource.NormalizeDeclaration(spec)
}

func mergeEntitySchema(base, generated manifest.DatasourceEntitySchema) manifest.DatasourceEntitySchema {
	return datasource.MergeEntitySchema(base, generated)
}

func normalizeDatasourceViews(views []manifest.DatasourceViewSpec) []manifest.DatasourceViewSpec {
	return datasource.NormalizeDeclaration(manifest.DatasourceSpec{Views: views}).Views
}

func normalizeDatasourceRelations(relations []manifest.DatasourceRelationSpec) []manifest.DatasourceRelationSpec {
	return datasource.NormalizeDeclaration(manifest.DatasourceSpec{Relations: relations}).Relations
}
