package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type apiClient struct {
	baseURL string
	key     string
	http    *http.Client
}

func newClient() *apiClient {
	return &apiClient{
		baseURL: apiURL,
		key:     apiKey,
		http:    &http.Client{},
	}
}

func (c *apiClient) do(method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("X-API-Key", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func (c *apiClient) get(path string, out interface{}) error {
	return c.do("GET", path, nil, out)
}

func (c *apiClient) post(path string, body, out interface{}) error {
	return c.do("POST", path, body, out)
}

func (c *apiClient) patch(path string, body, out interface{}) error {
	return c.do("PATCH", path, body, out)
}

func (c *apiClient) delete(path string) error {
	return c.do("DELETE", path, nil, nil)
}
