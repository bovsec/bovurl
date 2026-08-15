package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/you/bovurl/internal/ratelimit"
	"github.com/you/bovurl/internal/types"
)

// WaybackProvider, Internet Archive'in CDX API'sini kullanarak bir domain'e
// ait tüm arşivlenmiş URL'leri pasif olarak çeker. Gerçek hedefe hiç istek
// atmaz (tamamen pasif).
type WaybackProvider struct {
	client  *httpClientWrapper
	limiter *ratelimit.Limiter
}

type httpClientWrapper struct {
	timeoutSeconds int
}

// NewWayback yeni bir wayback provider oluşturur.
// perSecond: saniyede izin verilen istek sayısı (wayback genelde düşük tutulmalı).
func NewWayback(perSecond, timeoutSeconds int) *WaybackProvider {
	return &WaybackProvider{
		client:  &httpClientWrapper{timeoutSeconds: timeoutSeconds},
		limiter: ratelimit.New(perSecond),
	}
}

func (w *WaybackProvider) Name() string { return "wayback" }

func (w *WaybackProvider) Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error) {
	out := make(chan types.URLResult)
	errc := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errc)

		if err := w.limiter.Wait(ctx); err != nil {
			errc <- err
			return
		}

		client := newHTTPClient(w.client.timeoutSeconds)
		reqURL := fmt.Sprintf(
			"https://web.archive.org/cdx/search/cdx?url=*.%s/*&output=json&collapse=urlkey&fl=original&limit=100000",
			domain,
		)

		var body []byte
		err := ratelimit.WithBackoff(ctx, 3, func() error {
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
			errc <- fmt.Errorf("wayback: %w", err)
			return
		}

		// CDX JSON formati: [["original"], ["https://a.example.com/"], ["https://b.example.com/x"], ...]
		// İlk satır header (fl=original olduğunda ["original"]), onu atlıyoruz.
		var rows [][]string
		if err := json.Unmarshal(body, &rows); err != nil {
			errc <- fmt.Errorf("wayback: json parse hatasi: %w", err)
			return
		}

		for i, row := range rows {
			if i == 0 {
				continue // header satiri
			}
			if len(row) == 0 {
				continue
			}
			select {
			case out <- types.URLResult{
				URL:       row[0],
				Source:    "wayback",
				Domain:    domain,
				Timestamp: time.Now(),
			}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, errc
}
