// Package normalize, pipeline'dan akan URL'leri normalize eder, tekilleştirir
// (dedup) ve isteğe bağlı olarak benzer-görünümlü path'leri (katana'nın -fsu
// bayrağındaki mantık: /users/123 ve /users/456 gibi) filtreler.
//
// Tasarım kararı: tüm işlemler streaming (channel-tabanlı) yapılır. Binlerce
// URL biriktirip sona bırakmak yerine, akış sırasında tekilleştirme yapılır -
// bu, wayback gibi 100binlerce URL dönebilen kaynaklarla çalışırken bellek
// patlamasını önler.
package normalize

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/you/bovurl/internal/types"
)

// Deduper, normalize edilmiş URL anahtarına göre tekrar eden sonuçları eler.
// sync.Map kullanılır çünkü birden fazla goroutine (fan-in sonrası) aynı anda
// yazabilir.
type Deduper struct {
	seen sync.Map
}

func NewDeduper() *Deduper {
	return &Deduper{}
}

// Process, gelen kanaldaki her URL'i normalize eder; daha önce görülmemişse
// çıkış kanalına yazar.
func (d *Deduper) Process(in <-chan types.URLResult) <-chan types.URLResult {
	out := make(chan types.URLResult)
	go func() {
		defer close(out)
		for r := range in {
			key := NormalizeURL(r.URL)
			if key == "" {
				continue
			}
			if _, loaded := d.seen.LoadOrStore(key, true); !loaded {
				out <- r
			}
		}
	}()
	return out
}

// NormalizeURL bir URL'i tutarlı bir anahtara indirger:
//   - host küçük harfe çevrilir
//   - query parametreleri alfabetik sıralanır (aynı paramlar farklı sırayla
//     gelirse duplicate sayılmasın diye)
//   - fragment (#...) atılır
//   - trailing slash tutarlılığı saglanir (kök path haricinde)
func NormalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""

	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}

	q := u.Query()
	if len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sorted := url.Values{}
		for _, k := range keys {
			sorted[k] = q[k]
		}
		u.RawQuery = sorted.Encode()
	}

	return u.String()
}

// PathFilter, "benzer görünümlü" path'leri filtreler: bir path segmentinde
// (örn. /users/{id}/profile içindeki {id}) belirli bir eşiğin (threshold)
// üzerinde farklı değer görülürse, o segment "parametre" olarak öğrenilir ve
// sonraki eşleşen URL'ler filtrelenir (katana -fsu / -fst mantığı).
type PathFilter struct {
	mu        sync.Mutex
	threshold int
	// segmentValues: "path-onceki-segmentler-birlesimi" -> gorulen distinct degerler seti
	segmentValues map[string]map[string]bool
	promoted      map[string]bool // bu segment pozisyonu parametre olarak "terfi" etti mi
}

// NewPathFilter, threshold ile yeni bir filtre oluşturur (varsayılan 10,
// katana'daki -fst default'uyla aynı).
func NewPathFilter(threshold int) *PathFilter {
	if threshold <= 0 {
		threshold = 10
	}
	return &PathFilter{
		threshold:     threshold,
		segmentValues: make(map[string]map[string]bool),
		promoted:      make(map[string]bool),
	}
}

// Allow, verilen URL'in path'i daha once "parametre" olarak terfi etmis bir
// segment pozisyonuna denk geliyorsa false doner (yani filtrelenmeli).
func (p *PathFilter) Allow(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) == 0 {
		return true
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	prefix := u.Host
	for i, seg := range segments {
		posKey := prefix + "|" + strconv.Itoa(i)
		if p.promoted[posKey] {
			// Bu pozisyon zaten "parametre" - bu URL'i filtrele (yeni bir
			// path olarak sayma, cunku muhtemelen ayni endpoint'in ID varyanti).
			return false
		}
		if p.segmentValues[posKey] == nil {
			p.segmentValues[posKey] = make(map[string]bool)
		}
		p.segmentValues[posKey][seg] = true
		if len(p.segmentValues[posKey]) > p.threshold {
			p.promoted[posKey] = true
		}
		prefix = prefix + "/" + seg
	}
	return true
}
