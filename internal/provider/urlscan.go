package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/you/bovurl/internal/ratelimit"
	"github.com/you/bovurl/internal/types"
)

// URLScanProvider, urlscan.io search API'sini kullanir. API key olmadan da
// calisir ama rate limit dusuktur; APIKey verilirse "API-Key" header'i ile
// gonderilir ve daha yuksek limitlerden faydalanilir.
type URLScanProvider struct {
	timeoutSeconds int
	limiter        *ratelimit.Limiter
	apiKey         string
}

func NewURLScan(perSecond, timeoutSeconds int, apiKey string) *URLScanProvider {
	return &URLScanProvider{
		timeoutSeconds: timeoutSeconds,
		limiter:        ratelimit.New(perSecond),
		apiKey:         apiKey,
	}
}

func (u *URLScanProvider) Name() string { return "urlscan" }

type urlscanResult struct {
	Page struct {
		URL string `json:"url"`
	} `json:"page"`
	Sort []json.Number `json:"sort"`
}

type urlscanResponse struct {
	Results []urlscanResult `json:"results"`
	HasMore bool            `json:"has_more"`
}

func (u *URLScanProvider) Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error) {
	out := make(chan types.URLResult)
	errc := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errc)

		client := newHTTPClient(u.timeoutSeconds)
		searchAfter := ""
		const pageSize = 100
		const maxPages = 200

		for page := 0; page < maxPages; page++ {
			if err := u.limiter.Wait(ctx); err != nil {
				errc <- err
				return
			}

			reqURL := fmt.Sprintf("https://urlscan.io/api/v1/search/?q=domain:%s&size=%d", domain, pageSize)
			if searchAfter != "" {
				reqURL += "&search_after=" + searchAfter
			}

			var parsed urlscanResponse
			err := ratelimit.WithBackoff(ctx, 3, func() error {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
				if err != nil {
					return err
				}
				req.Header.Set("User-Agent", "bovurl/0.1.0 (+recon-tool)")
				if u.apiKey != "" {
					req.Header.Set("API-Key", u.apiKey)
				}
				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					return fmt.Errorf("urlscan status: %d", resp.StatusCode)
				}
				body, err := readAll(resp)
				if err != nil {
					return err
				}
				return json.Unmarshal(body, &parsed)
			})
			if err != nil {
				if page == 0 {
					errc <- fmt.Errorf("urlscan: %w", err)
				}
				return
			}

			if len(parsed.Results) == 0 {
				return
			}

			var lastSort []json.Number
			for _, r := range parsed.Results {
				if r.Page.URL == "" {
					continue
				}
				select {
				case out <- types.URLResult{
					URL:       r.Page.URL,
					Source:    "urlscan",
					Domain:    domain,
					Timestamp: time.Now(),
				}:
				case <-ctx.Done():
					return
				}
				lastSort = r.Sort
			}

			if !parsed.HasMore || len(lastSort) == 0 {
				return
			}
			parts := make([]string, len(lastSort))
			for i, v := range lastSort {
				parts[i] = v.String()
			}
			searchAfter = strings.Join(parts, ",")
		}
	}()

	return out, errc
}
