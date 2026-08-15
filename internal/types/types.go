package types

import "time"

// URLResult tüm pipeline boyunca akan tek ortak veri birimidir.
// Pasif kaynaklar, aktif crawler ve normalize katmanı hepsi bu tipi üretir/tüketir.
type URLResult struct {
	URL        string    `json:"url"`
	Source     string    `json:"source"`               // "wayback", "commoncrawl", "alienvault", "otx", "urlscan", "crtsh", "katana-active"
	Domain     string    `json:"domain"`                // sorgulanan kök domain
	Timestamp  time.Time `json:"timestamp"`
	StatusCode int       `json:"status_code,omitempty"` // aktif crawl sonrası doldurulur, pasifte 0
	Depth      int       `json:"depth,omitempty"`       // aktif crawl derinliği
	IsForm     bool      `json:"is_form,omitempty"`     // form-extraction sonucu mu
	FormFields []string  `json:"form_fields,omitempty"`
}

// Options tüm çalışma zamanı ayarlarını tek yerde toplar.
type Options struct {
	Domain          string
	OutputPath      string
	JSONLOutput     bool
	NoColor         bool
	Silent          bool
	Verbose         bool

	// Kaynak seçimi
	Sources        []string // boşsa hepsi
	ExcludeSources []string
	ActiveOnly     bool
	PassiveOnly    bool

	// Rate limit / performans
	GlobalRateLimit int // saniyede istek (pasif kaynaklar için global)
	Timeout         int // saniye, tek istek timeout
	MaxTimePerHost  int // dakika, provider başına üst sınır

	// Aktif crawl (katana tarzı)
	CrawlDepth      int
	Concurrency     int
	JSCrawl         bool
	FormExtraction  bool
	ExtensionFilter []string // bu uzantılar aktif crawl çıktısından filtrelenir
	FilterSimilar   bool     // katana -fsu benzeri: tekrarlayan path pattern'lerini filtrele
	SimilarThreshold int     // bir path segmentinde kaç farklı değerden sonra "parametre" sayılsın (varsayılan 10)
	UserAgent       string   // ozel User-Agent (bos ise varsayilan tarayici-benzeri UA kullanilir)

	// API anahtarları (opsiyonel, ortam değişkeninden de okunabilir)
	URLScanAPIKey string
}
