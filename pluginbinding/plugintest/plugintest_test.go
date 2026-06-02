package plugintest_test

import (
	"testing"

	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding/plugintest"
)

type input struct {
	Name string `json:"name" jsonschema:"required"`
}

type output struct {
	Message  string `json:"message"`
	Instance string `json:"instance"`
}

type datasourceInput struct {
	Query  string `json:"query,omitempty"`
	Entity string `json:"entity,omitempty"`
}

type datasourceOutput struct {
	Records []pluginbinding.DatasourceRecord `json:"records"`
	Count   int                              `json:"count"`
}

type datasourceLookupOutput struct {
	Matches []pluginbinding.LookupMatch[pluginbinding.DatasourceRecord] `json:"matches"`
	Count   int                                                         `json:"count"`
}

func TestRunOK(t *testing.T) {
	plugin := testPlugin()
	out := plugintest.RunOK[output](t, plugin, "test.hello", map[string]any{"name": "dex"}, plugintest.WithInstance("work"))
	if out.Message != "hello dex" || out.Instance != "work" {
		t.Fatalf("output = %#v", out)
	}
}

func TestRunError(t *testing.T) {
	plugin := testPlugin()
	err := plugintest.RunError(t, plugin, "test.fail", map[string]any{})
	if err.Code != "bad_input" {
		t.Fatalf("error = %#v", err)
	}
}

func TestDatasourceSearchOK(t *testing.T) {
	plugin := testDatasourcePlugin()
	out := plugintest.DatasourceSearchOK[datasourceOutput](t, plugin, map[string]any{"query": "dex", "entity": "test.item"}, plugintest.WithInstance("work"))
	if out.Count != 1 || out.Records[0].ID != "dex" || out.Records[0].Source.Instance != "work" {
		t.Fatalf("output = %#v", out)
	}
}

func TestDatasourceLookupOK(t *testing.T) {
	plugin := testDatasourcePlugin()
	out := plugintest.DatasourceLookupOK[datasourceLookupOutput](t, plugin, map[string]any{"query": "dex", "entity": "test.item"}, plugintest.WithInstance("work"))
	if out.Count != 1 || out.Matches[0].ID != "dex" || out.Matches[0].Source.Plugin != "test" || out.Matches[0].Source.Instance != "work" {
		t.Fatalf("output = %#v", out)
	}
}

func TestDatasourceSearchError(t *testing.T) {
	plugin := testDatasourcePlugin()
	err := plugintest.DatasourceSearchError(t, plugin, map[string]any{"entity": "other.item"})
	if err.Code != "bad_payload" {
		t.Fatalf("error = %#v", err)
	}
}

func testDatasourcePlugin() *pluginbinding.Plugin {
	searchSpec := pluginbinding.TypedDatasourceSpec[datasourceInput, datasourceOutput]("test.items", "test.item", "Test items.", []string{pluginbinding.CapabilitySearch})
	lookupSpec := pluginbinding.TypedDatasourceSpec[datasourceInput, datasourceLookupOutput]("test.items", "test.item", "Test items.", []string{pluginbinding.CapabilityLookup})
	return pluginbinding.Define(pluginbinding.ManifestSpec{Name: "test"},
		pluginbinding.RegisterDatasourceSearch(searchSpec, func(ctx pluginbinding.Context, input datasourceInput) (datasourceOutput, error) {
			record := pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", input.Query)
			return datasourceOutput{Records: []pluginbinding.DatasourceRecord{record}, Count: 1}, nil
		}),
		pluginbinding.RegisterDatasourceLookup(lookupSpec, func(ctx pluginbinding.Context, input datasourceInput) (datasourceLookupOutput, error) {
			source := pluginbinding.LookupSource{Source: "test", Plugin: "test", Instance: ctx.Request.Instance}
			record := pluginbinding.NewDatasourceRecord(ctx.DatasourceSource(), "test.item", input.Query)
			match := pluginbinding.LookupMatch[pluginbinding.DatasourceRecord]{Source: source, Entity: record.Entity, ID: record.ID, Score: 1000, MatchedFields: []string{"id"}, Record: record}
			return datasourceLookupOutput{Matches: []pluginbinding.LookupMatch[pluginbinding.DatasourceRecord]{match}, Count: 1}, nil
		}),
	)
}

func testPlugin() *pluginbinding.Plugin {
	helloSpec := pluginbinding.TypedOperationSpec[input, output]("test.hello", "Say hello.", pluginbinding.ReadOnly())
	failSpec := pluginbinding.TypedOperationSpec[struct{}, output]("test.fail", "Fail.", pluginbinding.ReadOnly())
	return pluginbinding.Define(pluginbinding.ManifestSpec{Name: "test"},
		pluginbinding.RegisterOperation(helloSpec, func(ctx pluginbinding.Context, input input) (output, error) {
			return output{Message: "hello " + input.Name, Instance: ctx.Request.Instance}, nil
		}),
		pluginbinding.RegisterOperation(failSpec, func(pluginbinding.Context, struct{}) (output, error) {
			return output{}, pluginbinding.Fail("bad_input", "failed")
		}),
	)
}
