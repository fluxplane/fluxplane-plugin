package fluxplaneplugin

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	"github.com/fluxplane/fluxplane-dex/core/pluginbinding"
	dexruntime "github.com/fluxplane/fluxplane-dex/runtime"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

type SystemCapabilityHost struct {
	System    fpsystem.System
	Workspace runtimeworkspace.Workspace
	Providers map[string]dexruntime.HostProvider
}

func NewSystemCapabilityHost(system fpsystem.System, workspace runtimeworkspace.Workspace, providers map[string]dexruntime.HostProvider) SystemCapabilityHost {
	return SystemCapabilityHost{System: system, Workspace: workspace, Providers: providers}
}

func (h SystemCapabilityHost) HTTP(ctx context.Context, input pluginbinding.HTTPRequest) (pluginbinding.HTTPResponse, error) {
	if h.System == nil || h.System.Network() == nil {
		return pluginbinding.HTTPResponse{}, fmt.Errorf("fluxplaneplugin: system network is unavailable")
	}
	requestURL, err := systemHTTPRequestURL(input)
	if err != nil {
		return pluginbinding.HTTPResponse{}, err
	}
	resp, err := systemkit.DoHTTP(ctx, h.System.Network(), systemkit.HTTPRequest{
		URL:       requestURL,
		Method:    input.Method,
		Headers:   input.Headers,
		Body:      input.Body,
		Timeout:   time.Duration(input.TimeoutMS) * time.Millisecond,
		MaxBytes:  input.MaxBytes,
		UserAgent: input.UserAgent,
	})
	if err != nil {
		return pluginbinding.HTTPResponse{}, err
	}
	return pluginbinding.HTTPResponse{
		URL:         resp.URL,
		FinalURL:    resp.FinalURL,
		Method:      resp.Method,
		Status:      resp.Status,
		StatusCode:  resp.StatusCode,
		Headers:     resp.Headers,
		ContentType: resp.ContentType,
		Body:        resp.Body,
		Truncated:   resp.Truncated,
		DurationMS:  int64(resp.Duration / time.Millisecond),
	}, nil
}

func systemHTTPRequestURL(input pluginbinding.HTTPRequest) (string, error) {
	endpoint := strings.TrimSpace(input.URL)
	if endpoint == "" {
		return "", fmt.Errorf("HTTP url is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("HTTP url must be absolute with a host")
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("HTTP url scheme must be http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("HTTP url must not include userinfo")
	}
	if strings.TrimSpace(input.Path) != "" {
		basePath := strings.TrimRight(parsed.Path, "/")
		baseEscapedPath := strings.TrimRight(parsed.EscapedPath(), "/")
		relPath := strings.TrimLeft(strings.TrimSpace(input.Path), "/")
		decodedRelPath, err := url.PathUnescape(relPath)
		if err != nil {
			return "", err
		}
		parsed.Path = basePath + "/" + decodedRelPath
		if baseEscapedPath != basePath || decodedRelPath != relPath {
			parsed.RawPath = baseEscapedPath + "/" + relPath
		}
	}
	query := parsed.Query()
	for key, values := range input.Query {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (h SystemCapabilityHost) BlobRead(ctx context.Context, input pluginbinding.BlobReadRequest) (pluginbinding.BlobReadResponse, error) {
	if h.Workspace == nil {
		return pluginbinding.BlobReadResponse{}, fmt.Errorf("fluxplaneplugin: workspace is unavailable")
	}
	data, truncated, resolved, err := h.Workspace.ReadFile(ctx, blobPath(input.Ref, input.Path), input.MaxBytes)
	if err != nil {
		return pluginbinding.BlobReadResponse{}, err
	}
	return pluginbinding.BlobReadResponse{
		Blob:      blobRef(resolved, int64(len(data))),
		Content:   data,
		Truncated: truncated,
	}, nil
}

func (h SystemCapabilityHost) BlobWrite(ctx context.Context, input pluginbinding.BlobWriteRequest) (pluginbinding.BlobRef, error) {
	writer, ok := h.Workspace.(fpsystem.BoundedFileWriter)
	if h.Workspace == nil || !ok {
		return pluginbinding.BlobRef{}, fmt.Errorf("fluxplaneplugin: workspace writer is unavailable")
	}
	resolved, err := writer.WriteFile(ctx, blobPath(input.Ref, input.Path), input.Content, 0o644, input.Overwrite)
	if err != nil {
		return pluginbinding.BlobRef{}, err
	}
	out := blobRef(resolved, int64(len(input.Content)))
	out.MediaType = strings.TrimSpace(input.MediaType)
	out.Filename = strings.TrimSpace(input.Filename)
	out.Metadata = cloneStringMap(input.Metadata)
	return out, nil
}

func (h SystemCapabilityHost) BlobInfo(ctx context.Context, input pluginbinding.BlobInfoRequest) (pluginbinding.BlobRef, error) {
	statFS, ok := h.Workspace.(fpsystem.BoundedStatFS)
	if h.Workspace == nil || !ok {
		return pluginbinding.BlobRef{}, fmt.Errorf("fluxplaneplugin: workspace stat is unavailable")
	}
	info, resolved, err := statFS.Stat(ctx, blobPath(input.Ref, input.Path))
	if err != nil {
		return pluginbinding.BlobRef{}, err
	}
	return blobRef(resolved, info.Size()), nil
}

func (h SystemCapabilityHost) EnvLookup(ctx context.Context, input pluginbinding.EnvLookupRequest) (pluginbinding.EnvLookupResponse, error) {
	key := strings.TrimSpace(input.Key)
	if key == "" {
		return pluginbinding.EnvLookupResponse{}, fmt.Errorf("environment key is empty")
	}
	if h.System == nil || h.System.Environment() == nil {
		return pluginbinding.EnvLookupResponse{}, fmt.Errorf("fluxplaneplugin: system environment is unavailable")
	}
	value, ok, err := h.System.Environment().Lookup(ctx, key)
	if err != nil {
		return pluginbinding.EnvLookupResponse{}, err
	}
	return pluginbinding.EnvLookupResponse{Key: key, Value: value, Found: ok}, nil
}

func (h SystemCapabilityHost) ProviderCall(ctx context.Context, input pluginbinding.ProviderCallRequest) (pluginbinding.ProviderCallResponse, error) {
	providerName := strings.TrimSpace(input.Provider)
	if providerName == "" {
		return pluginbinding.ProviderCallResponse{}, fmt.Errorf("provider is required")
	}
	provider := h.Providers[providerName]
	if provider == nil {
		return pluginbinding.ProviderCallResponse{}, fmt.Errorf("host provider %q is unavailable", providerName)
	}
	result, err := provider.Call(ctx, strings.TrimSpace(input.Action), input.Payload)
	if err != nil {
		return pluginbinding.ProviderCallResponse{}, err
	}
	return pluginbinding.ProviderCallResponse{Result: result}, nil
}

func blobPath(ref, path string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "workspace:") {
		return strings.TrimSpace(strings.TrimPrefix(ref, "workspace:"))
	}
	if ref != "" {
		return ref
	}
	return strings.TrimSpace(path)
}

func blobRef(resolved fpsystem.ResolvedPath, size int64) pluginbinding.BlobRef {
	return pluginbinding.BlobRef{
		Ref:  "workspace:" + resolved.Rel,
		Path: resolved.Rel,
		Size: size,
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

var _ dexruntime.CapabilityHost = SystemCapabilityHost{}
