package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/you/bovurl/internal/ratelimit"
	"github.com/you/bovurl/internal/types"
)

// CommonCrawlProvider, Common Crawl'in index API'sini kullanır.
// CDX'ten farkli olarak Common Crawl sonuclari NDJSON (satir basina bir JSON
// objesi) formatinda doner, tek bir JSON array degildir.
type CommonCrawlProvider struct {
	timeoutSeconds int
	limiter        *ratelimit.Limiter
}

func NewCommonCrawl(perSecond, timeoutSeconds int) *CommonCrawlProvider {
	return &CommonCrawlProvider{
		timeoutSeconds: timeoutSeconds,
		limiter:        ratelimit.New(perSecond),
	}
}

func (c *CommonCrawlProvider) Name() string { return "commoncrawl" }

type ccIndexEntry struct {
	ID  string `json:"id"`
	CDX string `json:"cdx-api"`
}

type ccRecord struct {
	URL string `json:"url"`
}

func (c *CommonCrawlProvider) Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error) {
	out := make(chan types.URLResult)
	errc := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errc)

		client := newHTTPClient(c.timeoutSeconds)

		// 1) Güncel index listesini çek, en son (ilk) girdiyi kullan.
		if err := c.limiter.Wait(ctx); err != nil {
			errc <- err
			return
		}
		var indexes []ccIndexEntry
		err := ratelimit.WithBackoff(ctx, 3, func() error {
			resp, err := doRequest(ctx, client, "https://index.commoncrawl.org/collinfo.json")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			body, err := readAll(resp)
			if err != nil {
				return err
			}
			return json.Unmarshal(body, &indexes)
		})
		if err != nil {
			errc <- fmt.Errorf("commoncrawl: index listesi alinamadi: %w", err)
			return
		}
		if len(indexes) == 0 {
			errc <- fmt.Errorf("commoncrawl: kullanilabilir index bulunamadi")
			return
		}

		// 2) En güncel indexte domain'i sorgula.
		latest := indexes[0]
		if err := c.limiter.Wait(ctx); err != nil {
			errc <- err
			return
		}
		queryURL := fmt.Sprintf("%s?url=*.%s/*&output=json", latest.CDX, domain)

		var body []byte
		err = ratelimit.WithBackoff(ctx, 3, func() error {
			resp, err := doRequest(ctx, client, queryURL)
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
			// Common Crawl'da bir domain hic bulunamazsa 404 doner - bu bir hata
			// degil, sadece "sonuc yok" demektir. Sessizce bitir.
			return
		}

		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // uzun satirlar icin buffer buyut
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var rec ccRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				continue // bozuk satiri atla, tum taramayi durdurma
			}
			if rec.URL == "" {
				continue
			}
			select {
			case out <- types.URLResult{
				URL:       rec.URL,
				Source:    "commoncrawl",
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
