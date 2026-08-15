package provider

import (
	"context"

	"github.com/you/bovurl/internal/ratelimit"
	"github.com/you/bovurl/internal/types"
)

// OTXProvider, AlienVaultProvider ile ayni alttaki API'yi kullanir (bkz.
// alienvault.go içindeki not). Ayri tutulmasinin nedeni kullaniciya kaynak
// bazli include/exclude (-s/-es) esnekligi saglamaktir; ileride farkli bir
// OTX endpoint'ine (orn. pulse/subscription API) gecilmek istenirse kod
// izole oldugu icin kolayca degistirilebilir.
type OTXProvider struct {
	timeoutSeconds int
	limiter        *ratelimit.Limiter
}

func NewOTX(perSecond, timeoutSeconds int) *OTXProvider {
	return &OTXProvider{timeoutSeconds: timeoutSeconds, limiter: ratelimit.New(perSecond)}
}

func (o *OTXProvider) Name() string { return "otx" }

func (o *OTXProvider) Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error) {
	return fetchOTXStyle(ctx, "otx", domain, o.timeoutSeconds, o.limiter)
}
