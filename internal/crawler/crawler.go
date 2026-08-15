// Package crawler, katana benzeri bir aktif crawl motoru sağlar: gerçek HTTP
// istekleri atar, HTML'den link çıkarır, JS dosyalarından endpoint arar,
// form alanlarını çıkarır. Harici bir HTML parser kütüphanesi (örn.
// golang.org/x/net/html) kullanmıyoruz - bunun yerine hedefli regex'ler
// kullanıyoruz. Bu MVP için yeterlidir ama gerçek bir DOM parser kadar sağlam
// değildir (örn. çok bozuk/iç içe HTML'de kaçırılan linkler olabilir). Modül
// erişimi olduğunda x/net/html veya doğrudan katana'nın kendi crawler
// kütüphanesi (github.com/projectdiscovery/katana/pkg/engine/standard) ile
// bu paket değiştirilebilir - Provider/Crawler arayüzü aynı kaldığı sürece
// geri kalan pipeline etkilenmez.
package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/you/bovurl/internal/normalize"
	"github.com/you/bovurl/internal/ratelimit"
	"github.com/you/bovurl/internal/types"
)

var (
	// href/src/action ceken genel regex - HTML attribute tabanli.
	linkRe = regexp.MustCompile(`(?i)(?:href|src|action)\s*=\s*["']([^"'#\s]+)["']`)

	// JS dosyalarinda endpoint gibi gorunen string literal'leri yakalar.
	// LinkFinder/getJS gibi araclarin kullandigi yaklasima benzer, basitlestirilmis.
	jsEndpointRe = regexp.MustCompile(`["'](/[a-zA-Z0-9_\-./]{2,}|https?://[a-zA-Z0-9_\-.]+(?:/[a-zA-Z0-9_\-./%?=&#]*)?)["']`)

	// <form ...action="...">...</form> bloklarini yakalar (case-insensitive, dotall).
	formRe = regexp.MustCompile(`(?is)<form[^>]*?action\s*=\s*["']([^"']*)["'][^>]*>(.*?)</form>`)

	// form icindeki <input name="..."> alanlarini yakalar.
	inputNameRe = regexp.MustCompile(`(?i)<input[^>]*name\s*=\s*["']([^"']+)["']`)
)

// job, worker pool icinde islenmeyi bekleyen tek bir crawl gorevini temsil eder.
type job struct {
	url   string
	depth int
}

// Crawler, aktif crawl fazini yonetir.
type Crawler struct {
	opts       *types.Options
	client     *http.Client
	// hostLimiters: her host icin ayri AdaptiveLimiter - bir hostun
	// yavaslatilmasi/engellenmesi digerlerini etkilemez, ve her host kendi
	// blok/basari gecmisine gore bagimsiz hizlanir/yavaslar.
	hostLimiters sync.Map // host string -> *ratelimit.AdaptiveLimiter
	visited      sync.Map // normalize edilmemis ham URL -> bool (basit visited seti)
	pathFilter   *normalize.PathFilter
}

// New, verilen ayarlarla bir Crawler olusturur.
func New(opts *types.Options) *Crawler {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10
	}
	c := &Crawler{
		opts:   opts,
		client: &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}
	if opts.FilterSimilar {
		c.pathFilter = normalize.NewPathFilter(opts.SimilarThreshold)
	}
	return c
}

// limiterFor, verilen host icin AdaptiveLimiter dondurur - yoksa olusturur.
// GlobalRateLimit <=0 ise guvenli varsayilan (AdaptiveLimiter icinde saniyede
// 3 istek) kullanilir, kullanici hicbir flag vermese bile taramanin
// engellenmeden tamamlanmasi hedeflenir.
func (c *Crawler) limiterFor(host string) *ratelimit.AdaptiveLimiter {
	if v, ok := c.hostLimiters.Load(host); ok {
		return v.(*ratelimit.AdaptiveLimiter)
	}
	l := ratelimit.NewAdaptive(c.opts.GlobalRateLimit)
	actual, _ := c.hostLimiters.LoadOrStore(host, l)
	return actual.(*ratelimit.AdaptiveLimiter)
}

// Run, pasif katmandan gelen seed URL'leri (seeds) alir, her birini kok
// olarak BFS ile tarar ve zenginlestirilmis sonuclari out kanalina yazar.
// Fonksiyon, seeds kanali kapanip tum kuyruklanmis islerin bitmesini
// bekledikten sonra out'u kapatir.
func (c *Crawler) Run(ctx context.Context, seeds <-chan types.URLResult) <-chan types.URLResult {
	out := make(chan types.URLResult, 100)

	// jobs kanali buyuk bir buffer ile aciliyor: worker'lar yeni bulunan
	// linkleri bu kanala geri yazarken bloklanmasin diye. Coklu-domain, yuksek
	// derinlikli taramalarda bu buffer yetersiz kalirsa arttirilmali (bkz.
	// paket yorumundaki not) - production'da bounded queue + backpressure
	// mekanizmasi onerilir.
	const jobBuffer = 200000
	jobs := make(chan job, jobBuffer)

	var wg sync.WaitGroup

	// Seed besleme fazi icin "placeholder" gorev: wg sayaci, seed kanali tam
	// olarak kapanip tuketilene kadar asla sifira dusmemeli - aksi halde
	// worker'lar henuz gelmemis seed'leri beklemeden erken sonlanabilir.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := range seeds {
			if c.shouldSkip(s.URL) {
				continue
			}
			wg.Add(1)
			jobs <- job{url: s.URL, depth: 0}
		}
	}()

	// Tum isler (seed besleme dahil) bittiginde jobs kanalini kapat.
	go func() {
		wg.Wait()
		close(jobs)
	}()

	concurrency := c.opts.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	var workerWG sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for j := range jobs {
				c.process(ctx, j, out, jobs, &wg)
				wg.Done()
			}
		}()
	}

	go func() {
		workerWG.Wait()
		close(out)
	}()

	return out
}

// shouldSkip, MaxDomainPages limitine ulasilmis mi veya zaten ziyaret
// edilmis mi kontrolu yapar; ExtensionFilter'a uyan statik dosyalari da eler.
func (c *Crawler) shouldSkip(rawURL string) bool {
	if _, already := c.visited.LoadOrStore(rawURL, true); already {
		return true
	}
	lower := strings.ToLower(rawURL)
	for _, ext := range c.opts.ExtensionFilter {
		if strings.HasSuffix(lower, "."+strings.ToLower(ext)) {
			return true
		}
	}
	return false
}

// process, tek bir job'i isler: HTTP istegi atar, sonucu out'a yazar, HTML
// ise link/form cikarir, JS ise endpoint arar; scope icindeki yeni linkleri
// derinlik+1 ile tekrar jobs kanalina kuyruklar.
func (c *Crawler) process(ctx context.Context, j job, out chan<- types.URLResult, jobs chan<- job, wg *sync.WaitGroup) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	parsedURL, err := url.Parse(j.url)
	if err != nil {
		return
	}
	limiter := c.limiterFor(parsedURL.Hostname())

	if err := limiter.Wait(ctx); err != nil {
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return
	}
	setBrowserLikeHeaders(req, c.opts.UserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		// Baglanti hatasi (timeout/reset) da bir "blok" sinyali sayilir -
		// hedef asiri yuklenmis veya bizi engelliyor olabilir.
		if warn := limiter.ReportStatus(true); warn {
			fmt.Fprintf(os.Stderr, "[!] %s yavaslatiliyor: ardisik baglanti hatasi/blok sinyali alindi, hiz otomatik dusuruldu\n", parsedURL.Hostname())
		}
		return
	}
	defer resp.Body.Close()

	// 400/403/429 uc kodu da WAF/bot-korumasi tarafindan "block" sayfasi
	// olarak kullanilabiliyor (bazi WAF'lar klasik 403 yerine 400 donuyor) -
	// kullanicinin bildirdigi spesifik durum bu yuzden 400 de dahil edildi.
	blocked := resp.StatusCode == 429 || resp.StatusCode == 403 || resp.StatusCode == 400
	if warn := limiter.ReportStatus(blocked); warn {
		fmt.Fprintf(os.Stderr, "[!] %s yavaslatiliyor: ardisik 400/403/429 sinyali alindi, hiz otomatik dusuruldu\n", parsedURL.Hostname())
	}

	result := types.URLResult{
		URL:        j.url,
		Source:     "active",
		Timestamp:  time.Now(),
		StatusCode: resp.StatusCode,
		Depth:      j.depth,
	}
	select {
	case out <- result:
	case <-ctx.Done():
		return
	}

	if j.depth >= c.opts.CrawlDepth {
		return // derinlik siniri - yeni link kuyruklamiyoruz
	}

	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024)) // 8MB ust sinir
	if err != nil {
		return
	}
	bodyStr := string(body)

	base := parsedURL

	isJS := strings.Contains(contentType, "javascript") || strings.HasSuffix(strings.ToLower(base.Path), ".js")

	if isJS && c.opts.JSCrawl {
		c.extractJSEndpoints(ctx, bodyStr, base, j.depth, out, jobs, wg)
		return
	}

	if strings.Contains(contentType, "html") || contentType == "" {
		c.extractLinks(bodyStr, base, j.depth, jobs, wg)
		if c.opts.FormExtraction {
			c.extractForms(bodyStr, base, out)
		}
	}
}

// extractLinks, HTML icindeki href/src/action degerlerini bulur, ayni scope
// (domain) icindeyse ve daha once ziyaret edilmediyse yeni job olarak kuyruklar.
func (c *Crawler) extractLinks(body string, base *url.URL, depth int, jobs chan<- job, wg *sync.WaitGroup) {
	matches := linkRe.FindAllStringSubmatch(body, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		resolved := resolveURL(base, m[1])
		if resolved == "" {
			continue
		}
		if !inScope(resolved, base.Hostname()) {
			continue
		}
		if c.shouldSkip(resolved) {
			continue
		}
		if c.pathFilter != nil && !c.pathFilter.Allow(resolved) {
			continue
		}
		wg.Add(1)
		jobs <- job{url: resolved, depth: depth + 1}
	}
}

// extractJSEndpoints, bir JS dosyasi icindeki string literal'lerden
// endpoint'e benzeyenleri bulur ve scope icindeyse yeni job olarak kuyruklar.
func (c *Crawler) extractJSEndpoints(ctx context.Context, body string, base *url.URL, depth int, out chan<- types.URLResult, jobs chan<- job, wg *sync.WaitGroup) {
	matches := jsEndpointRe.FindAllStringSubmatch(body, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		resolved := resolveURL(base, m[1])
		if resolved == "" || !inScope(resolved, base.Hostname()) {
			continue
		}
		select {
		case out <- types.URLResult{
			URL:       resolved,
			Source:    "active-js",
			Timestamp: time.Now(),
			Depth:     depth,
		}:
		case <-ctx.Done():
			return
		}
		if c.shouldSkip(resolved) {
			continue
		}
		wg.Add(1)
		jobs <- job{url: resolved, depth: depth + 1}
	}
}

// extractForms, <form> bloklarini ve icindeki input alan adlarini cikarir.
func (c *Crawler) extractForms(body string, base *url.URL, out chan<- types.URLResult) {
	forms := formRe.FindAllStringSubmatch(body, -1)
	for _, f := range forms {
		if len(f) < 3 {
			continue
		}
		action := f[1]
		inner := f[2]
		resolved := resolveURL(base, action)
		if resolved == "" {
			resolved = base.String()
		}

		var fields []string
		for _, im := range inputNameRe.FindAllStringSubmatch(inner, -1) {
			if len(im) >= 2 {
				fields = append(fields, im[1])
			}
		}

		out <- types.URLResult{
			URL:        resolved,
			Source:     "active-form",
			Timestamp:  time.Now(),
			IsForm:     true,
			FormFields: fields,
		}
	}
}

// resolveURL, base sayfaya gore goreli bir linki mutlak URL'e cevirir.
func resolveURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "javascript:") || strings.HasPrefix(ref, "mailto:") || strings.HasPrefix(ref, "data:") {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(u)
	return resolved.String()
}

// inScope, verilen URL'in host'unun kok domain ile ayni scope'ta olup
// olmadigini kontrol eder (kok domain veya herhangi bir subdomain).
func inScope(rawURL, rootHost string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	root := strings.ToLower(rootHost)
	return host == root || strings.HasSuffix(host, "."+root)
}

// setBrowserLikeHeaders, gercek bir tarayicinin gonderdigi header setine
// yakin bir set uygular. Amac: "bariz script/bot" gibi gorunup rate-limit'e
// bile gerek kalmadan ilk istekte 400/403 ile reddedilmeyi onlemek.
//
// ONEMLI SINIRLAMA: Bu, HTTP header seviyesinde bir iyilestirmedir - TLS
// ClientHello imzasi (JA3/JA4 fingerprint) hala Go'nun varsayilan net/http
// implementasyonuna ait olacagindan, JA3 bazli gelismis bot-korumalari
// (Cloudflare/Akamai gibi) yine de ayirt edebilir. Bu, IP/kimlik gizleme
// (proxychains vb.) ile COZULECEK bir sorun degildir - dogru cozum ya
// tarama IP'sini hedefte allowlist'e aldirmak ya da (ileri asama) ozel bir
// TLS fingerprint kutuphanesi (utls benzeri) kullanmaktir.
//
// Accept-Encoding BILINCLI OLARAK set edilmiyor: Go'nun http.Transport'u
// varsayilan olarak "gzip" gonderip yaniti otomatik acar; bu header'i elle
// set edersek (orn. "gzip, deflate, br") otomatik decompress devre disi
// kalir ve br (brotli) Go stdlib'de desteklenmedigi icin yanit bozuk
// (garbled) gelir. Bu yuzden bu header kasitli olarak atlanmistir.
func setBrowserLikeHeaders(req *http.Request, customUA string) {
	ua := customUA
	if ua == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Cache-Control", "no-cache")
}

