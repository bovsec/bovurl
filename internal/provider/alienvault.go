package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/you/bovurl/internal/ratelimit"
	"github.com/you/bovurl/internal/types"
)

// AlienVaultProvider, AlienVault OTX'in url_list endpoint'ini kullanir.
//
// NOT: gau/urlfinder gibi araclarda "alienvault" ve "otx" ayni API'yi
// (otx.alienvault.com) farkli isimlerle listeler - tarihsel/isimlendirme
// nedenleriyle iki ayri kaynak gibi gorunur ama alttaki veri kaynagi aynidir.
// Biz de ayni davranisi koruyoruz (bkz. otx.go) cunku bazi kullanicilar
// birini digerinden hariç tutmak isteyebiliyor (-es alienvault gibi).
type AlienVaultProvider struct {
	timeoutSeconds int
	limiter        *ratelimit.Limiter
}

func NewAlienVault(perSecond, timeoutSeconds int) *AlienVaultProvider {
	return &AlienVaultProvider{timeoutSeconds: timeoutSeconds, limiter: ratelimit.New(perSecond)}
}

func (a *AlienVaultProvider) Name() string { return "alienvault" }

func (a *AlienVaultProvider) Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error) {
	return fetchOTXStyle(ctx, "alienvault", domain, a.timeoutSeconds, a.limiter)
}

type otxURLListResponse struct {
	URLList []struct {
		URL string `json:"url"`
	} `json:"url_list"`
	HasNext bool `json:"has_next"`
}

// fetchOTXStyle, AlienVault ve OTX provider'lari tarafindan paylasilan ortak
// pagination mantigidir (kod tekrarini onlemek icin).
func fetchOTXStyle(ctx context.Context, sourceName, domain string, timeoutSeconds int, limiter *ratelimit.Limiter) (<-chan types.URLResult, <-chan error) {
	out := make(chan types.URLResult)
	errc := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errc)

		client := newHTTPClient(timeoutSeconds)
		page := 1
		const limitPerPage = 100
		const maxPages = 500 // guvenlik siniri - sonsuz donguyu engelle

		for page <= maxPages {
			if err := limiter.Wait(ctx); err != nil {
				errc <- err
				return
			}

			reqURL := fmt.Sprintf(
				"https://otx.alienvault.com/api/v1/indicators/domain/%s/url_list?limit=%d&page=%d",
				domain, limitPerPage, page,
			)

			var parsed otxURLListResponse
			err := ratelimit.WithBackoff(ctx, 3, func() error {
				resp, err := doRequest(ctx, client, reqURL)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				body, err := readAll(resp)
				if err != nil {
					return err
				}
				return json.Unmarshal(body, &parsed)
			})
			if err != nil {
				if page == 1 {
					errc <- fmt.Errorf("%s: %w", sourceName, err)
				}
				return
			}

			for _, item := range parsed.URLList {
				if item.URL == "" {
					continue
				}
				select {
				case out <- types.URLResult{
					URL:       item.URL,
					Source:    sourceName,
					Domain:    domain,
					Timestamp: time.Now(),
				}:
				case <-ctx.Done():
					return
				}
			}

			if !parsed.HasNext {
				return
			}
			page++
		}
	}()

	return out, errc
}
