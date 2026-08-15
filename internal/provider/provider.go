// Package provider, pasif URL/subdomain kaynaklarının (wayback, commoncrawl,
// alienvault, otx, urlscan, crt.sh) ortak arayüzünü ve yardımcı HTTP istemcisini
// tanımlar. Yeni bir kaynak eklemek için tek yapılması gereken bu interface'i
// implement etmektir - orkestrasyon katmanı (pipeline) değişmeden yeni kaynağı
// devreye alabilir.
package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/you/bovurl/internal/types"
)

// maxBodyBytes, tek bir provider yanıtı için okunacak azami bayt sayısı.
// Bazı kaynaklar (özellikle wayback) çok büyük domainlerde devasa JSON
// dönebilir; bellek patlamasını önlemek için üst sınır koyuyoruz.
const maxBodyBytes = 64 * 1024 * 1024 // 64MB

// readAll, response body'sini üst sınır dahilinde okur.
func readAll(resp *http.Response) ([]byte, error) {
	limited := io.LimitReader(resp.Body, maxBodyBytes)
	return io.ReadAll(limited)
}

// Provider her pasif kaynağın uyması gereken sözleşmedir.
// Fetch, sonuçları ve olası hataları streaming (channel) olarak döner - böylece
// wayback gibi 100binlerce URL dönebilen kaynaklar bellekte birikmez.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error)
}

// sharedHTTPClient tüm provider'lar tarafından kullanılan, makul timeout'lu
// ortak istemci. Her provider kendi context timeout'unu da ayrıca uygular.
func newHTTPClient(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	return &http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	}
}

// doRequest, User-Agent set edilmiş bir GET isteğini yapar ve status code
// 200 değilse anlamlı bir hata döner. Tüm provider'lar bunu kullanır.
func doRequest(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "bovurl/0.1.0 (+recon-tool)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("beklenmeyen status code: %d (%s)", resp.StatusCode, url)
	}
	return resp, nil
}
