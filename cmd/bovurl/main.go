// bovurl: pasif + aktif URL kesif araci.
//
// Mimari (asama asama):
//  1) Pasif katman: wayback, commoncrawl, alienvault, otx, urlscan, crtsh
//     kaynaklarindan (Provider interface) URL/subdomain toplanir, hepsi
//     paralel calisir ve fan-in ile tek bir kanalda birlestirilir.
//  2) Normalize+Dedup: URL'ler normalize edilip tekillestirilir (streaming).
//  3) Aktif katman (opsiyonel): pasif katmandan gelen her URL/subdomain seed
//     olarak katana-tarzi bir BFS crawler'a verilir; HTML linkleri, JS
//     endpoint'leri ve form alanlari cikarilir.
//  4) Tekrar normalize+dedup (aktif faz yeni URL'ler bulur).
//  5) Output: JSONL veya duz metin olarak diske/stdout'a streaming yazilir.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/you/bovurl/internal/banner"
	"github.com/you/bovurl/internal/crawler"
	"github.com/you/bovurl/internal/normalize"
	"github.com/you/bovurl/internal/output"
	"github.com/you/bovurl/internal/provider"
	"github.com/you/bovurl/internal/types"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		domain         = flag.String("d", "", "hedef domain (orn. example.com)")
		domainLong     = flag.String("domain", "", "hedef domain (uzun form)")
		listFile       = flag.String("list", "", "birden fazla domain iceren dosya (satir basina bir domain)")
		outPath        = flag.String("o", "", "cikti dosyasi (bos ise stdout)")
		jsonl          = flag.Bool("j", false, "JSONL formatinda cikti")
		sourcesFlag    = flag.String("s", "", "kullanilacak pasif kaynaklar, virgulle ayrik (varsayilan: hepsi)")
		excludeFlag    = flag.String("es", "", "haric tutulacak pasif kaynaklar, virgulle ayrik")
		passiveOnly    = flag.Bool("passive-only", false, "sadece pasif toplama yap, aktif crawl atla")
		activeOnly     = flag.Bool("active-only", false, "sadece aktif crawl yap (seed URL'ler stdin'den okunur)")
		depth          = flag.Int("depth", 3, "aktif crawl azami derinlik")
		concurrency    = flag.Int("c", 10, "aktif crawl concurrency (ayni anda islenen istek sayisi)")
		jsCrawl        = flag.Bool("jc", true, "JS dosyalarindan endpoint cikar")
		formExtract    = flag.Bool("fx", true, "form alanlarini cikar")
		extFilter      = flag.String("ef", "png,jpg,jpeg,gif,svg,css,woff,woff2,ico", "aktif crawl'da atlanacak uzantilar, virgulle ayrik")
		filterSimilar  = flag.Bool("fsu", false, "benzer gorunumlu path'leri filtrele (orn. /users/123, /users/456)")
		similarThresh  = flag.Int("fst", 10, "fsu icin bir segmentte kac farkli deger sonrasi 'parametre' sayilsin")
		rateLimit      = flag.Int("rl", 5, "pasif kaynaklar icin saniye basina istek limiti (kaynak basina)")
		timeout        = flag.Int("timeout", 15, "istek basina saniye cinsinden timeout")
		noColor        = flag.Bool("nc", false, "renkli ciktiyi kapat")
		silent         = flag.Bool("silent", false, "sadece sonuc URL'lerini bas (banner/ilerleme yok)")
		urlscanKey     = flag.String("urlscan-key", "", "urlscan.io API anahtari (opsiyonel, env URLSCAN_API_KEY de okunur)")
		globalTimeoutM = flag.Int("max-time", 0, "tum tarama icin dakika cinsinden ust sinir (0 = sinirsiz)")
		userAgent      = flag.String("ua", "", "ozel User-Agent (bos ise varsayilan tarayici-benzeri UA kullanilir - engellenmeyi azaltmak icin)")
	)
	flag.Parse()

	if *domain == "" {
		*domain = *domainLong
	}
	if *urlscanKey == "" {
		*urlscanKey = os.Getenv("URLSCAN_API_KEY")
	}

	if !*silent {
		banner.Print(*noColor)
		banner.Warn(*noColor)
	}

	if *domain == "" && *listFile == "" && !*activeOnly {
		fmt.Fprintln(os.Stderr, "hata: -d/-domain veya -list belirtilmeli")
		flag.Usage()
		return 1
	}

	domains, err := collectDomains(*domain, *listFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hata:", err)
		return 1
	}

	opts := &types.Options{
		Domain:          strings.Join(domains, ","),
		OutputPath:      *outPath,
		JSONLOutput:     *jsonl,
		NoColor:         *noColor,
		Silent:          *silent,
		Sources:         splitCSV(*sourcesFlag),
		ExcludeSources:  splitCSV(*excludeFlag),
		ActiveOnly:      *activeOnly,
		PassiveOnly:     *passiveOnly,
		GlobalRateLimit: *rateLimit,
		Timeout:         *timeout,
		CrawlDepth:      *depth,
		Concurrency:     *concurrency,
		JSCrawl:         *jsCrawl,
		FormExtraction:  *formExtract,
		ExtensionFilter: splitCSV(*extFilter),
		FilterSimilar:   *filterSimilar,
		SimilarThreshold: *similarThresh,
		UserAgent:       *userAgent,
		URLScanAPIKey:   *urlscanKey,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C ile duzgun (graceful) kapanis: mevcut goroutine'ler context
	// cancel ile bilgilendirilir, o ana kadar toplanan sonuclar zaten
	// streaming yazildigi icin kaybolmaz.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigc
		fmt.Fprintln(os.Stderr, "\n[!] Durduruluyor, mevcut isler tamamlaniyor...")
		cancel()
	}()

	if *globalTimeoutM > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, time.Duration(*globalTimeoutM)*time.Minute)
		defer timeoutCancel()
	}

	// --- 1) Pasif katman (veya active-only ise stdin'den seed okuma) ---
	var passiveOut <-chan types.URLResult

	if opts.ActiveOnly {
		passiveOut = readSeedsFromStdin(domains)
	} else {
		providers := buildProviders(opts)
		if !opts.Silent {
			names := make([]string, 0, len(providers))
			for _, p := range providers {
				names = append(names, p.Name())
			}
			fmt.Fprintf(os.Stderr, "[INF] Pasif kaynaklar: %s\n", strings.Join(names, ", "))
			fmt.Fprintf(os.Stderr, "[INF] Hedef domain(ler): %s\n", strings.Join(domains, ", "))
		}
		passiveOut = runPassiveAll(ctx, domains, providers)
	}

	// --- 2) Normalize + Dedup (pasif faz sonrasi) ---
	dedupedPassive := normalize.NewDeduper().Process(passiveOut)

	// --- 3) Aktif katman (opsiyonel) ---
	var finalChan <-chan types.URLResult
	if opts.PassiveOnly {
		finalChan = dedupedPassive
	} else {
		c := crawler.New(opts)
		activeOut := c.Run(ctx, dedupedPassive)
		finalChan = normalize.NewDeduper().Process(activeOut)
	}

	// --- 4) Output ---
	count, err := output.Write(finalChan, opts.OutputPath, opts.JSONLOutput, opts.Silent)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hata:", err)
		return 1
	}

	if !opts.Silent {
		fmt.Fprintf(os.Stderr, "\n[INF] Toplam %d benzersiz URL bulundu.\n", count)
	}
	return 0
}

// collectDomains, -d ve/veya -list'ten hedef domain listesini olusturur.
func collectDomains(single, listFile string) ([]string, error) {
	var domains []string
	if single != "" {
		domains = append(domains, strings.TrimSpace(single))
	}
	if listFile != "" {
		f, err := os.Open(listFile)
		if err != nil {
			return nil, fmt.Errorf("liste dosyasi acilamadi: %w", err)
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			domains = append(domains, line)
		}
	}
	return domains, nil
}

// readSeedsFromStdin, -active-only modunda stdin'den (katana -list gibi)
// dogrudan URL listesi okur, pasif fazi atlar.
func readSeedsFromStdin(domains []string) <-chan types.URLResult {
	out := make(chan types.URLResult)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			d := ""
			if len(domains) > 0 {
				d = domains[0]
			}
			out <- types.URLResult{URL: line, Source: "stdin-seed", Domain: d, Timestamp: time.Now()}
		}
	}()
	return out
}

// splitCSV, virgulle ayrilmis bir string'i temiz bir []string'e cevirir.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// allProviderFactories, desteklenen tum pasif kaynaklarin isim -> olusturucu
// eslemesidir. Yeni bir kaynak eklemek icin sadece buraya bir satir eklemek
// yeterlidir; geri kalan pipeline (fan-in, dedup, -s/-es filtreleme) otomatik
// calisir.
func allProviderFactories(opts *types.Options) map[string]provider.Provider {
	return map[string]provider.Provider{
		"wayback":     provider.NewWayback(opts.GlobalRateLimit, opts.Timeout),
		"commoncrawl": provider.NewCommonCrawl(opts.GlobalRateLimit, opts.Timeout),
		"alienvault":  provider.NewAlienVault(opts.GlobalRateLimit, opts.Timeout),
		"otx":         provider.NewOTX(opts.GlobalRateLimit, opts.Timeout),
		"urlscan":     provider.NewURLScan(opts.GlobalRateLimit, opts.Timeout, opts.URLScanAPIKey),
		"crtsh":       provider.NewCrtSh(opts.GlobalRateLimit, opts.Timeout),
	}
}

// buildProviders, -s/-es bayraklarina gore aktif olacak provider listesini olusturur.
func buildProviders(opts *types.Options) []provider.Provider {
	all := allProviderFactories(opts)

	excluded := make(map[string]bool)
	for _, e := range opts.ExcludeSources {
		excluded[strings.ToLower(e)] = true
	}

	var selected []string
	if len(opts.Sources) > 0 {
		selected = opts.Sources
	} else {
		for name := range all {
			selected = append(selected, name)
		}
	}

	var result []provider.Provider
	for _, name := range selected {
		name = strings.ToLower(strings.TrimSpace(name))
		if excluded[name] {
			continue
		}
		if p, ok := all[name]; ok {
			result = append(result, p)
		}
	}
	return result
}

// runPassiveAll, her domain icin her provider'i paralel calistirir ve
// hepsini tek bir kanalda fan-in ile birlestirir.
func runPassiveAll(ctx context.Context, domains []string, providers []provider.Provider) <-chan types.URLResult {
	merged := make(chan types.URLResult)
	var wg sync.WaitGroup

	for _, d := range domains {
		for _, p := range providers {
			wg.Add(1)
			go func(domain string, p provider.Provider) {
				defer wg.Done()
				out, errc := p.Fetch(ctx, domain)
				for {
					select {
					case r, ok := <-out:
						if !ok {
							return
						}
						select {
						case merged <- r:
						case <-ctx.Done():
							return
						}
					case err, ok := <-errc:
						if ok && err != nil {
							fmt.Fprintf(os.Stderr, "[WRN] %s (%s): %v\n", p.Name(), domain, err)
						}
					case <-ctx.Done():
						return
					}
				}
			}(d, p)
		}
	}

	go func() {
		wg.Wait()
		close(merged)
	}()

	return merged
}
