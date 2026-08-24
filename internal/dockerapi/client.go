package dockerapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("docker object not found")

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func New(host string) (*Client, error) {
	if strings.HasPrefix(host, "tcp://") {
		host = "http://" + strings.TrimPrefix(host, "tcp://")
	}
	baseURL, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parse DOCKER_HOST: %w", err)
	}
	if (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, fmt.Errorf("DOCKER_HOST must be a valid tcp, http, or https URL")
	}
	baseURL.Path = strings.TrimSuffix(baseURL.Path, "/")
	return &Client{
		baseURL: baseURL,
		http: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		}},
	}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerInspect, error) {
	var result ContainerInspect
	err := c.do(ctx, http.MethodGet, "/containers/"+pathSegment(id)+"/json", nil, nil, &result)
	return result, err
}

func (c *Client) InspectImage(ctx context.Context, name string) (ImageInspect, error) {
	var result ImageInspect
	err := c.do(ctx, http.MethodGet, "/images/"+pathSegment(name)+"/json", nil, nil, &result)
	return result, err
}

func (c *Client) PullImage(ctx context.Context, name string) error {
	query := url.Values{"fromImage": []string{name}}
	return c.do(ctx, http.MethodPost, "/images/create", query, nil, nil)
}

func (c *Client) CreateContainer(ctx context.Context, name string, request ContainerCreateRequest) (ContainerCreateResponse, error) {
	query := url.Values{"name": []string{name}}
	var result ContainerCreateResponse
	err := c.do(ctx, http.MethodPost, "/containers/create", query, request, &result)
	return result, err
}

func (c *Client) ConnectNetwork(ctx context.Context, network, container string) error {
	request := struct {
		Container      string            `json:"Container"`
		EndpointConfig *EndpointSettings `json:"EndpointConfig"`
	}{Container: container, EndpointConfig: &EndpointSettings{}}
	return c.do(ctx, http.MethodPost, "/networks/"+pathSegment(network)+"/connect", nil, request, nil)
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+pathSegment(id)+"/start", nil, struct{}{}, nil)
}

func (c *Client) StreamContainerLogs(ctx context.Context, id string, stdout, stderr io.Writer) error {
	query := url.Values{
		"follow": []string{"true"},
		"stderr": []string{"true"},
		"stdout": []string{"true"},
		"tail":   []string{"all"},
	}
	return c.do(ctx, http.MethodGet, "/containers/"+pathSegment(id)+"/logs", query, nil, containerLogStream{stdout: stdout, stderr: stderr})
}

func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	seconds := int((timeout + time.Second - 1) / time.Second)
	query := url.Values{"t": []string{strconv.Itoa(seconds)}}
	return c.do(ctx, http.MethodPost, "/containers/"+pathSegment(id)+"/stop", query, struct{}{}, nil)
}

func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	query := url.Values{"force": []string{"false"}, "v": []string{"false"}}
	return c.do(ctx, http.MethodDelete, "/containers/"+pathSegment(id), query, nil, nil)
}

func (c *Client) ListManagedContainers(ctx context.Context, parentID string) ([]ContainerSummary, error) {
	filter, err := json.Marshal(map[string][]string{
		"label": {"wakewrap.managed=true", "wakewrap.parent=" + parentID},
	})
	if err != nil {
		return nil, err
	}
	query := url.Values{"all": []string{"true"}, "filters": []string{string(filter)}}
	var result []ContainerSummary
	err = c.do(ctx, http.MethodGet, "/containers/json", query, nil, &result)
	return result, err
}

func (c *Client) do(ctx context.Context, method, endpoint string, query url.Values, requestBody, result any) error {
	target := *c.baseURL
	decodedEndpoint, err := url.PathUnescape(endpoint)
	if err != nil {
		return fmt.Errorf("decode Docker endpoint: %w", err)
	}
	target.RawPath = target.EscapedPath() + endpoint
	target.Path += decodedEndpoint
	target.RawQuery = query.Encode()

	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode Docker request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return fmt.Errorf("create Docker request: %w", err)
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Docker %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		apiErr := fmt.Errorf("Docker %s %s: status %d: %s", method, endpoint, resp.StatusCode, strings.TrimSpace(string(message)))
		if resp.StatusCode == http.StatusNotFound {
			return errors.Join(ErrNotFound, apiErr)
		}
		return apiErr
	}
	if result == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	if stream, ok := result.(containerLogStream); ok {
		if err := stream.copy(resp.Body); err != nil {
			return fmt.Errorf("decode Docker response for %s: %w", endpoint, err)
		}
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode Docker response for %s: %w", endpoint, err)
	}
	return nil
}

type containerLogStream struct {
	stdout io.Writer
	stderr io.Writer
}

func (s containerLogStream) copy(source io.Reader) error {
	var header [8]byte
	for {
		if _, err := io.ReadFull(source, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		var destination io.Writer
		switch header[0] {
		case 0, 1:
			destination = s.stdout
		case 2:
			destination = s.stderr
		default:
			return fmt.Errorf("unknown Docker log stream %d", header[0])
		}
		if _, err := io.CopyN(destination, source, int64(binary.BigEndian.Uint32(header[4:]))); err != nil {
			return err
		}
	}
}

func pathSegment(value string) string {
	return url.PathEscape(value)
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
