package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/you/bovurl/internal/ratelimit"
	"github.com/you/bovurl/internal/types"
)

// CrtShProvider, crt.sh Certificate Transparency log arayuzunden subdomain
// toplar. Bu kaynak URL degil, subdomain doner - ciktida "https://" onekiyle
// bir seed URL olarak isaretlenir ki aktif crawl fazina dogrudan girdi
// olarak verilebilsin.
//
// DIKKAT: crt.sh resmi bir rate-limit belirtmez ama agresif paralel istekte
// sik sik 503 doner ve IP gecici olarak yavaslatilabilir. Bu yuzden dusuk
// bir limiter + backoff zorunlu.
type CrtShProvider struct {
	timeoutSeconds int
	limiter        *ratelimit.Limiter
}

func NewCrtSh(perSecond, timeoutSeconds int) *CrtShProvider {
	return &CrtShProvider{timeoutSeconds: timeoutSeconds, limiter: ratelimit.New(perSecond)}
}

func (c *CrtShProvider) Name() string { return "crtsh" }

type crtShEntry struct {
	NameValue string `json:"name_value"`
	CommonName string `json:"common_name"`
}

func (c *CrtShProvider) Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error) {
	out := make(chan types.URLResult)
	errc := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errc)

		if err := c.limiter.Wait(ctx); err != nil {
			errc <- err
			return
		}

		client := newHTTPClient(c.timeoutSeconds)
		reqURL := fmt.Sprintf("https://crt.sh/?q=%%25.%s&output=json", domain)

		var body []byte
		err := ratelimit.WithBackoff(ctx, 4, func() error {
			resp, err := doRequest(ctx, client, reqURL)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			b, err := readAll(resp)
			if err != nil {
				return err
			}
			body = b
			return nil
		})
		if err != nil {
			errc <- fmt.Errorf("crtsh: %w", err)
			return
		}

		var entries []crtShEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			errc <- fmt.Errorf("crtsh: json parse hatasi: %w", err)
			return
		}

		// name_value alani cok satirli olabilir (bir sertifikada birden fazla
		// SAN/domain kaydi), her satiri ayri subdomain olarak ele almaliyiz.
		// Ayrica wildcard (*.sub.domain.com) kayitlarini temizliyoruz.
		seen := make(map[string]bool)
		for _, e := range entries {
			lines := strings.Split(e.NameValue, "\n")
			for _, ln := range lines {
				sub := strings.TrimSpace(ln)
				sub = strings.TrimPrefix(sub, "*.")
				if sub == "" || seen[sub] {
					continue
				}
				seen[sub] = true

				select {
				case out <- types.URLResult{
					URL:       "https://" + sub,
					Source:    "crtsh",
					Domain:    domain,
					Timestamp: time.Now(),
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, errc
}
