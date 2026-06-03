package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	sdkdatasource "github.com/fluxplane/fluxplane-plugin/datasource"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type indexSnapshot struct {
	Plugin    management.Ref    `json:"plugin"`
	Instance  string            `json:"instance"`
	Index     string            `json:"index"`
	Records   []json.RawMessage `json:"records"`
	UpdatedAt time.Time         `json:"updated_at"`
	Metadata  json.RawMessage   `json:"metadata,omitempty"`
}

type indexRecord struct {
	Entity        string                     `json:"entity,omitempty"`
	ID            string                     `json:"id"`
	Title         string                     `json:"title,omitempty"`
	URL           string                     `json:"url,omitempty"`
	Links         map[string]string          `json:"links,omitempty"`
	Origin        sdkdatasource.LookupSource `json:"origin,omitempty"`
	Score         int                        `json:"score,omitempty"`
	MatchedFields []string                   `json:"matched_fields,omitempty"`
	Record        json.RawMessage            `json:"record,omitempty"`
}

// BuildIndex asks the plugin runtime to build index records and stores them locally.
func (b *Backend) BuildIndex(ctx context.Context, req management.IndexBuildRequest) (management.IndexBuildResult, error) {
	plugin, err := b.installedPlugin(req.Ref)
	if err != nil {
		return management.IndexBuildResult{}, err
	}
	instance := normalizeInstance(req.Instance)
	input := map[string]any{}
	if strings.TrimSpace(req.Index) != "" {
		input["index"] = strings.TrimSpace(req.Index)
	}
	if strings.TrimSpace(req.Entity) != "" {
		input["entity"] = strings.TrimSpace(req.Entity)
	}
	inputRaw, err := json.Marshal(input)
	if err != nil {
		return management.IndexBuildResult{}, err
	}
	resp, err := b.invokePlugin(ctx, plugin, instance, protocol.CommandOperationsCall, protocol.OperationCall{Name: plugin.Ref.Name + ".index.build", Input: inputRaw})
	if err != nil {
		return management.IndexBuildResult{}, err
	}
	indexes, err := b.decodeIndexBuildResults(ctx, plugin, instance, resp.Result)
	if err != nil {
		return management.IndexBuildResult{}, err
	}
	out := management.IndexBuildResult{Plugin: req.Ref, Instance: instance, Stored: !req.DryRun}
	for _, snapshot := range indexes {
		out.Indexes = append(out.Indexes, snapshot.Index)
		out.Records += len(snapshot.Records)
		if snapshot.UpdatedAt.After(out.UpdatedAt) {
			out.UpdatedAt = snapshot.UpdatedAt
		}
		if req.DryRun {
			continue
		}
		if err := b.saveIndexSnapshot(snapshot); err != nil {
			return management.IndexBuildResult{}, err
		}
	}
	sort.Strings(out.Indexes)
	if len(out.Indexes) == 1 {
		out.Index = out.Indexes[0]
	}
	if req.DryRun {
		out.Message = "dry run"
	}
	return out, nil
}

// IndexStatus returns local index snapshot status.
func (b *Backend) IndexStatus(_ context.Context, req management.IndexStatusRequest) (management.IndexStatusResult, error) {
	instance := normalizeInstance(req.Instance)
	if strings.TrimSpace(req.Ref.Name) != "" {
		status, err := b.indexStatus(req.Ref, instance)
		if err != nil {
			return management.IndexStatusResult{}, err
		}
		return management.IndexStatusResult{Plugin: req.Ref, Instance: instance, Indexes: []management.IndexStatus{status}}, nil
	}
	plugins, err := b.ListPlugins(context.Background(), management.ListRequest{All: true})
	if err != nil {
		return management.IndexStatusResult{}, err
	}
	out := management.IndexStatusResult{Instance: instance, Status: map[string]management.IndexStatus{}}
	for _, plugin := range plugins {
		status, err := b.indexStatus(plugin.Ref, instance)
		if err != nil {
			return management.IndexStatusResult{}, err
		}
		out.Status[plugin.Ref.Key()] = status
	}
	return out, nil
}

func (b *Backend) callIndexedDatasource(plugin storedPlugin, instance, command string, input json.RawMessage) (json.RawMessage, bool, error) {
	snapshots, err := b.loadIndexSnapshots(plugin.Ref, instance)
	if err != nil {
		return nil, false, err
	}
	if !hasIndexRecords(snapshots) {
		return nil, false, nil
	}
	switch command {
	case protocol.CommandDatasourcesSearch:
		options := decodeIndexSearchInput(input)
		selected, handled := selectedIndexSnapshots(snapshots, options.Datasource, options.Entity)
		if !handled {
			return nil, false, nil
		}
		records := searchIndexRecords(selected, options)
		result := sdkdatasource.NewSearchResult("host_index", options.Query, records)
		raw, err := json.Marshal(result)
		return raw, true, err
	case protocol.CommandDatasourcesLookup:
		options := decodeIndexLookupInput(input)
		selected, handled := selectedIndexSnapshots(snapshots, options.Datasource, options.Entity)
		if !handled {
			return nil, false, nil
		}
		matches := lookupIndexRecords(selected, options)
		result := sdkdatasource.NewLookupResult("host_index", options.Text, sdkdatasource.LookupTerms(options), matches)
		raw, err := json.Marshal(result)
		return raw, true, err
	case protocol.CommandDatasourcesGet:
		options := decodeIndexGetInput(input)
		selected, handled := selectedIndexSnapshots(snapshots, options.Datasource, options.Entity)
		if !handled {
			return nil, false, nil
		}
		if strings.TrimSpace(options.ID) == "" {
			return nil, true, fmt.Errorf("fluxplane-plugin: datasource get requires id")
		}
		record, ok := getIndexRecord(selected, options)
		if !ok {
			return nil, true, fmt.Errorf("fluxplane-plugin: indexed record %q not found", options.ID)
		}
		result := sdkdatasource.NewGetResult("host_index", record)
		raw, err := json.Marshal(result)
		return raw, true, err
	default:
		return nil, false, nil
	}
}

func (b *Backend) decodeIndexBuildResults(ctx context.Context, plugin storedPlugin, instance string, raw json.RawMessage) ([]indexSnapshot, error) {
	var result struct {
		Index   string          `json:"index,omitempty"`
		Records json.RawMessage `json:"records,omitempty"`
		Indexes []struct {
			Index    string          `json:"index,omitempty"`
			Records  json.RawMessage `json:"records,omitempty"`
			Metadata json.RawMessage `json:"metadata,omitempty"`
		} `json:"indexes,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	indexes := result.Indexes
	if len(indexes) == 0 {
		indexName := strings.TrimSpace(result.Index)
		if indexName == "" {
			indexName = b.defaultIndexName(ctx, plugin)
		}
		indexes = append(indexes, struct {
			Index    string          `json:"index,omitempty"`
			Records  json.RawMessage `json:"records,omitempty"`
			Metadata json.RawMessage `json:"metadata,omitempty"`
		}{Index: indexName, Records: result.Records})
	}
	now := time.Now().UTC()
	out := make([]indexSnapshot, 0, len(indexes))
	for _, index := range indexes {
		indexName := strings.TrimSpace(index.Index)
		if indexName == "" {
			return nil, fmt.Errorf("fluxplane-plugin: index build result did not include index name")
		}
		records, err := indexRecords(index.Records)
		if err != nil {
			return nil, fmt.Errorf("fluxplane-plugin: normalize index %q records: %w", indexName, err)
		}
		out = append(out, indexSnapshot{
			Plugin:    plugin.Ref,
			Instance:  instance,
			Index:     indexName,
			Records:   records,
			UpdatedAt: now,
			Metadata:  normalizeIndexMetadata(index.Metadata, plugin.Ref, instance, indexName, len(records), now),
		})
	}
	return out, nil
}

func (b *Backend) defaultIndexName(ctx context.Context, plugin storedPlugin) string {
	resp, err := b.invokePlugin(ctx, plugin, management.DefaultInstance, protocol.CommandManifest, nil)
	if err == nil {
		var manifest sdkmanifest.PluginManifest
		if json.Unmarshal(resp.Result, &manifest) == nil && len(manifest.Indexes) > 0 {
			if name := strings.TrimSpace(manifest.Indexes[0].Name); name != "" {
				return name
			}
		}
	}
	return plugin.Ref.Name + ".index"
}

func (b *Backend) saveIndexSnapshot(snapshot indexSnapshot) error {
	path := b.indexPath(snapshot.Plugin, snapshot.Instance, snapshot.Index)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func (b *Backend) indexStatus(ref management.Ref, instance string) (management.IndexStatus, error) {
	snapshots, err := b.loadIndexSnapshots(ref, instance)
	if err != nil {
		return management.IndexStatus{}, err
	}
	status := management.IndexStatus{Plugin: ref, Instance: instance}
	for _, snapshot := range snapshots {
		status.Indexes = append(status.Indexes, snapshot.Index)
		status.Records += len(snapshot.Records)
		if snapshot.UpdatedAt.After(status.UpdatedAt) {
			status.UpdatedAt = snapshot.UpdatedAt
		}
		status.Details = append(status.Details, management.IndexStatusEntry{
			Index:     snapshot.Index,
			Records:   len(snapshot.Records),
			UpdatedAt: snapshot.UpdatedAt,
			Metadata:  copyRaw(snapshot.Metadata),
		})
	}
	sort.Strings(status.Indexes)
	sort.Slice(status.Details, func(i, j int) bool { return status.Details[i].Index < status.Details[j].Index })
	return status, nil
}

func (b *Backend) loadIndexSnapshots(ref management.Ref, instance string) ([]indexSnapshot, error) {
	dir := filepath.Join(b.indexDir(), pathSegment(ref.Key()), pathSegment(normalizeInstance(instance)))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshots []indexSnapshot
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var snapshot indexSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Index < snapshots[j].Index })
	return snapshots, nil
}

func hasIndexRecords(snapshots []indexSnapshot) bool {
	for _, snapshot := range snapshots {
		if len(snapshot.Records) > 0 {
			return true
		}
	}
	return false
}

func selectedIndexSnapshots(snapshots []indexSnapshot, datasourceName, entity string) ([]indexSnapshot, bool) {
	datasourceName = strings.TrimSpace(datasourceName)
	entity = strings.TrimSpace(entity)
	if datasourceName != "" {
		var selected []indexSnapshot
		for _, snapshot := range snapshots {
			if snapshot.Index == datasourceName {
				selected = append(selected, snapshot)
			}
		}
		return selected, len(selected) > 0
	}
	if entity == "" {
		return snapshots, hasIndexRecords(snapshots)
	}
	for _, snapshot := range snapshots {
		for _, raw := range snapshot.Records {
			record, ok := normalizeIndexRecord(raw)
			if ok && record.Entity == entity {
				return snapshots, true
			}
		}
	}
	return nil, false
}

func decodeIndexSearchInput(raw json.RawMessage) sdkdatasource.SearchInput {
	var input sdkdatasource.SearchInput
	_ = json.Unmarshal(raw, &input)
	return input
}

func decodeIndexLookupInput(raw json.RawMessage) sdkdatasource.LookupInput {
	var input sdkdatasource.LookupInput
	_ = json.Unmarshal(raw, &input)
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	if input.Text == "" {
		input.Text = firstInputString(object, "query", "q")
	}
	if input.Datasource == "" {
		input.Datasource = firstInputString(object, "datasource")
	}
	if input.Entity == "" {
		input.Entity = firstInputString(object, "entity")
	}
	if term := firstInputString(object, "term", "id", "ref", "key", "url"); term != "" {
		input.Terms = append(input.Terms, term)
	}
	return input
}

func decodeIndexGetInput(raw json.RawMessage) sdkdatasource.GetInput {
	var input sdkdatasource.GetInput
	_ = json.Unmarshal(raw, &input)
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	if input.ID == "" {
		input.ID = firstInputString(object, "ref", "key")
	}
	if input.Datasource == "" {
		input.Datasource = firstInputString(object, "datasource")
	}
	if input.Entity == "" {
		input.Entity = firstInputString(object, "entity")
	}
	return input
}

func searchIndexRecords(snapshots []indexSnapshot, options sdkdatasource.SearchInput) []indexRecord {
	query := strings.ToLower(strings.TrimSpace(options.Query))
	entity := strings.TrimSpace(options.Entity)
	limit := options.Limit
	if limit <= 0 {
		limit = 20
	}
	var out []indexRecord
	for _, snapshot := range snapshots {
		for _, raw := range snapshot.Records {
			record, ok := normalizeIndexRecord(raw)
			if !ok {
				continue
			}
			record = enrichIndexRecord(snapshot, record)
			if entity != "" && record.Entity != entity {
				continue
			}
			score, fields := indexRecordScore(record, query)
			if query == "" || score > 0 {
				record.Score = score
				record.MatchedFields = fields
				out = append(out, record)
			}
		}
	}
	sortIndexRecords(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func lookupIndexRecords(snapshots []indexSnapshot, options sdkdatasource.LookupInput) []sdkdatasource.LookupMatch[indexRecord] {
	entity := strings.TrimSpace(options.Entity)
	candidates := make([]sdkdatasource.LookupCandidate, 0)
	for _, snapshot := range snapshots {
		for _, raw := range snapshot.Records {
			record, ok := normalizeIndexRecord(raw)
			if !ok {
				continue
			}
			record = enrichIndexRecord(snapshot, record)
			if entity != "" && record.Entity != entity {
				continue
			}
			candidates = append(candidates, sdkdatasource.NewLookupCandidate(record.Origin, record.Entity, record.ID, record, indexRecordLookupValues(record)))
		}
	}
	matches := sdkdatasource.LookupMatches(options, candidates)
	out := make([]sdkdatasource.LookupMatch[indexRecord], 0, len(matches))
	for _, match := range matches {
		record, _ := match.Record.(indexRecord)
		out = append(out, sdkdatasource.NewLookupMatch(match.Source, match.Entity, match.ID, match.Score, match.MatchedFields, record))
	}
	return out
}

func getIndexRecord(snapshots []indexSnapshot, options sdkdatasource.GetInput) (indexRecord, bool) {
	id := strings.TrimSpace(options.ID)
	entity := strings.TrimSpace(options.Entity)
	for _, snapshot := range snapshots {
		for _, raw := range snapshot.Records {
			record, ok := normalizeIndexRecord(raw)
			if !ok || record.ID != id {
				continue
			}
			record = enrichIndexRecord(snapshot, record)
			if entity != "" && record.Entity != entity {
				continue
			}
			return record, true
		}
	}
	return indexRecord{}, false
}

func (b *Backend) indexDir() string {
	return filepath.Join(filepath.Dir(b.path), "indexes")
}

func (b *Backend) indexPath(ref management.Ref, instance, index string) string {
	return filepath.Join(b.indexDir(), pathSegment(ref.Key()), pathSegment(normalizeInstance(instance)), pathSegment(index)+".json")
}

func indexRecords(raw json.RawMessage) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var records []json.RawMessage
	if err := json.Unmarshal(raw, &records); err == nil {
		return records, nil
	}
	return []json.RawMessage{copyRaw(raw)}, nil
}

func normalizeIndexMetadata(raw json.RawMessage, ref management.Ref, instance, index string, records int, updatedAt time.Time) json.RawMessage {
	metadata := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &metadata)
	}
	defaults := map[string]any{
		"built_at": updatedAt.Format(time.RFC3339),
		"plugin":   ref.Name,
		"instance": instance,
		"index":    index,
		"records":  records,
	}
	for key, value := range defaults {
		if _, ok := metadata[key]; !ok {
			metadata[key] = value
		}
	}
	out, _ := json.Marshal(metadata)
	return out
}

func normalizeIndexRecord(raw json.RawMessage) (indexRecord, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return indexRecord{}, false
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return indexRecord{}, false
	}
	fields := mergedRecordFields(object)
	id := firstRecordString(fields, "id", "key", "ref", "path_with_namespace", "web_url", "project_id")
	if id == "" {
		return indexRecord{}, false
	}
	record := indexRecord{
		Entity: firstRecordString(fields, "entity"),
		ID:     id,
	}
	record = applyStandardIndexFields(record, fields)
	record.Record = cleanIndexRecordRaw(fields)
	return record, true
}

func enrichIndexRecord(snapshot indexSnapshot, record indexRecord) indexRecord {
	var object map[string]any
	_ = json.Unmarshal(record.Record, &object)
	if record.Title == "" || record.URL == "" || len(record.Links) == 0 {
		record = applyStandardIndexFields(record, object)
	}
	record.Record = cleanIndexRecordRaw(object)
	record.Origin = sdkdatasource.LookupSource{
		Source:   "host_index",
		Plugin:   snapshot.Plugin.Name,
		Instance: snapshot.Instance,
		Index:    snapshot.Index,
	}
	return record
}

func mergedRecordFields(object map[string]any) map[string]any {
	out := map[string]any{}
	if nested, ok := object["record"].(map[string]any); ok {
		for key, value := range nested {
			out[key] = value
		}
	}
	for key, value := range object {
		out[key] = value
	}
	return out
}

func cleanIndexRecordRaw(object map[string]any) json.RawMessage {
	if object == nil {
		return nil
	}
	clean := map[string]any{}
	for key, value := range object {
		switch key {
		case "entity", "id", "title", "url", "links", "origin", "score", "matched_fields", "source":
			continue
		default:
			clean[key] = value
		}
	}
	raw, _ := json.Marshal(clean)
	return raw
}

func applyStandardIndexFields(record indexRecord, object map[string]any) indexRecord {
	if record.Title == "" {
		record.Title = firstRecordString(object, "title", "name", "name_with_namespace")
	}
	if record.URL == "" {
		record.URL = firstRecordString(object, "url", "web_url", "html_url")
	}
	links := indexRecordLinks(record, object)
	if len(record.Links) == 0 {
		record.Links = links
	} else {
		for key, value := range links {
			if _, ok := record.Links[key]; !ok {
				record.Links[key] = value
			}
		}
	}
	return record
}

func indexRecordLinks(record indexRecord, object map[string]any) map[string]string {
	links := map[string]string{}
	if record.URL != "" {
		links["self"] = record.URL
	}
	if rawLinks, ok := object["links"].(map[string]any); ok {
		for key, value := range rawLinks {
			if text := anyString(value); text != "" {
				links[key] = text
			}
		}
	}
	if len(links) == 0 {
		return nil
	}
	return links
}

func indexRecordScore(record indexRecord, query string) (int, []string) {
	if query == "" {
		return 1, nil
	}
	score := 0
	var fields []string
	add := func(field, value string, exact, prefix, contains int) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		switch {
		case value == query:
			score = max(score, exact)
			fields = appendMatchedField(fields, field)
		case strings.HasPrefix(value, query):
			score = max(score, prefix)
			fields = appendMatchedField(fields, field)
		case strings.Contains(value, query):
			score = max(score, contains)
			fields = appendMatchedField(fields, field)
		}
	}
	add("id", record.ID, 1000, 850, 650)
	add("title", record.Title, 950, 800, 600)
	add("entity", record.Entity, 500, 400, 300)
	for key, value := range record.Links {
		add("links."+key, value, 450, 350, 250)
	}
	var object map[string]any
	_ = json.Unmarshal(record.Record, &object)
	for _, key := range []string{"username", "name", "display_name", "real_name", "name_with_namespace", "path_with_namespace", "full_name", "full_path", "reference", "author_username", "web_url", "email", "state", "user_id", "channel_id"} {
		add("record."+key, firstRecordString(object, key), 900, 750, 550)
	}
	if strings.Contains(strings.ToLower(string(record.Record)), query) {
		score = max(score, 100)
		fields = appendMatchedField(fields, "record")
	}
	if score == 0 {
		termScore, termFields := indexRecordTokenScore(record, query)
		if termScore > 0 {
			score = termScore
			fields = appendMatchedFields(fields, termFields...)
		}
	}
	return score, fields
}

func indexRecordTokenScore(record indexRecord, query string) (int, []string) {
	terms := searchTerms(query)
	if len(terms) < 2 {
		return 0, nil
	}
	score := 0
	var fields []string
	for _, term := range terms {
		termScore, termFields := indexRecordScore(record, term)
		if termScore == 0 {
			return 0, nil
		}
		score += termScore
		fields = appendMatchedFields(fields, termFields...)
	}
	return score / len(terms), fields
}

func searchTerms(query string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, token := range strings.Fields(strings.ToLower(strings.TrimSpace(query))) {
		token = strings.Trim(token, " \t\n\r\"'()[]{}<>.,;:#!")
		if len(token) < 2 || indexStopword(token) || seen[token] {
			continue
		}
		seen[token] = true
		terms = append(terms, token)
	}
	return terms
}

func indexRecordLookupValues(record indexRecord) map[string]string {
	values := map[string]string{
		"id":     record.ID,
		"title":  record.Title,
		"url":    record.URL,
		"entity": record.Entity,
	}
	for key, value := range record.Links {
		values["links."+key] = value
	}
	var object map[string]any
	_ = json.Unmarshal(record.Record, &object)
	for _, key := range []string{"username", "name", "display_name", "name_with_namespace", "path_with_namespace", "full_name", "full_path", "reference", "author_username", "web_url", "email", "state", "user_id", "channel_id"} {
		values["record."+key] = firstRecordString(object, key)
	}
	return values
}

func sortIndexRecords(records []indexRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Score != records[j].Score {
			return records[i].Score > records[j].Score
		}
		if records[i].Entity != records[j].Entity {
			return records[i].Entity < records[j].Entity
		}
		return records[i].ID < records[j].ID
	})
}

func firstInputString(object map[string]any, keys ...string) string {
	return firstRecordString(object, keys...)
}

func firstRecordString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := anyString(object[key]); value != "" {
			return value
		}
	}
	return ""
}

func anyString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case int:
		return strconv.Itoa(value)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func appendMatchedField(fields []string, field string) []string {
	return appendMatchedFields(fields, field)
}

func appendMatchedFields(fields []string, candidates ...string) []string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		exists := false
		for _, field := range fields {
			if field == candidate {
				exists = true
				break
			}
		}
		if !exists {
			fields = append(fields, candidate)
		}
	}
	return fields
}

func indexStopword(token string) bool {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "look", "lookup", "find", "open", "see", "the", "for", "from", "this", "that", "please", "at", "in", "to", "and", "with":
		return true
	default:
		return false
	}
}

func pathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
