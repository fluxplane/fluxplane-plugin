package pluginbinding

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type HostHTTPClientOption func(*hostHTTPClientOptions)

type hostHTTPClientOptions struct {
	auth        *HTTPAuthRequest
	endpointRef string
	timeoutMS   int
	maxBytes    int
}

func HostHTTPClient(host HostClient, options ...HostHTTPClientOption) *http.Client {
	cfg := hostHTTPClientOptions{}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	return &http.Client{Transport: hostHTTPTransport{host: host, options: cfg}}
}

func HostHTTPClientAuth(auth HTTPAuthRequest) HostHTTPClientOption {
	return func(options *hostHTTPClientOptions) {
		authCopy := auth
		options.auth = &authCopy
	}
}

func HostHTTPClientEndpointRef(endpointRef string) HostHTTPClientOption {
	return func(options *hostHTTPClientOptions) {
		options.endpointRef = strings.TrimSpace(endpointRef)
	}
}

func HostHTTPClientTimeout(timeoutMS int) HostHTTPClientOption {
	return func(options *hostHTTPClientOptions) {
		options.timeoutMS = timeoutMS
	}
}

func HostHTTPClientMaxBytes(maxBytes int) HostHTTPClientOption {
	return func(options *hostHTTPClientOptions) {
		options.maxBytes = maxBytes
	}
}

type hostHTTPTransport struct {
	host    HostClient
	options hostHTTPClientOptions
}

func (t hostHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.host == nil {
		return nil, fmt.Errorf("host client is unavailable")
	}
	var body []byte
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = raw
		_ = req.Body.Close()
	}
	headers := map[string]string{}
	for key, values := range req.Header {
		if len(values) == 0 {
			continue
		}
		headers[key] = strings.Join(values, ", ")
	}
	userAgent := req.UserAgent()
	input := HTTPRequest{
		Method:    req.Method,
		Headers:   headers,
		Body:      body,
		Auth:      t.options.auth,
		TimeoutMS: t.options.timeoutMS,
		MaxBytes:  t.options.maxBytes,
		UserAgent: userAgent,
	}
	if t.options.endpointRef != "" {
		input.EndpointRef = t.options.endpointRef
		input.Path = req.URL.EscapedPath()
		input.Query = map[string][]string(req.URL.Query())
	} else {
		input.URL = req.URL.String()
	}
	response, err := t.host.HTTP(input)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	for key, values := range response.Headers {
		for _, value := range values {
			header.Add(key, value)
		}
	}
	statusCode := response.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	status := response.Status
	if strings.TrimSpace(status) == "" {
		status = strconv.Itoa(statusCode) + " " + http.StatusText(statusCode)
	}
	return &http.Response{
		Status:        status,
		StatusCode:    statusCode,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(response.Body)),
		ContentLength: int64(len(response.Body)),
		Request:       req,
	}, nil
}
