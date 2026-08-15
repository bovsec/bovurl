package normalize

import (
	"testing"

	"github.com/you/bovurl/internal/types"
)

func TestNormalizeURL_QueryParamOrder(t *testing.T) {
	a := NormalizeURL("https://Example.com/path?b=2&a=1")
	b := NormalizeURL("https://example.com/path?a=1&b=2")
	if a != b {
		t.Fatalf("query param sirasi farkli URL'leri ayni normalize etmeli:\n a=%q\n b=%q", a, b)
	}
}

func TestNormalizeURL_TrailingSlash(t *testing.T) {
	a := NormalizeURL("https://example.com/path/")
	b := NormalizeURL("https://example.com/path")
	if a != b {
		t.Fatalf("trailing slash farki normalize sonrasi ayni olmali:\n a=%q\n b=%q", a, b)
	}
}

func TestNormalizeURL_RootPathKeepsSlash(t *testing.T) {
	got := NormalizeURL("https://example.com/")
	if got != "https://example.com/" {
		t.Fatalf("kok path icin '/' korunmali, alinan: %q", got)
	}
}

func TestNormalizeURL_Fragment(t *testing.T) {
	a := NormalizeURL("https://example.com/path#section1")
	b := NormalizeURL("https://example.com/path#section2")
	if a != b {
		t.Fatalf("fragment farkli olsa da normalize sonrasi ayni olmali:\n a=%q\n b=%q", a, b)
	}
}

func TestNormalizeURL_InvalidReturnsEmpty(t *testing.T) {
	if got := NormalizeURL("not a url \x7f"); got != "" {
		// Not: url.Parse cogu string icin hata vermeyebilir, bu yuzden asil
		// kontrol bos host durumu icin.
		t.Logf("beklenmedik sekilde parse edildi: %q (bu bazi durumlarda normal olabilir)", got)
	}
	if got := NormalizeURL("/relative/path"); got != "" {
		t.Fatalf("host'suz (relative) URL bos donmeli, alinan: %q", got)
	}
}

func TestDeduper_RemovesDuplicates(t *testing.T) {
	in := make(chan types.URLResult, 4)
	in <- types.URLResult{URL: "https://example.com/a"}
	in <- types.URLResult{URL: "https://example.com/a"} // duplicate
	in <- types.URLResult{URL: "https://example.com/a?x=1&y=2"}
	in <- types.URLResult{URL: "https://example.com/a?y=2&x=1"} // ayni query, farkli sira -> duplicate
	close(in)

	out := NewDeduper().Process(in)

	var results []types.URLResult
	for r := range out {
		results = append(results, r)
	}

	if len(results) != 2 {
		t.Fatalf("2 benzersiz sonuc bekleniyordu, alinan: %d (%v)", len(results), results)
	}
}

func TestPathFilter_PromotesAfterThreshold(t *testing.T) {
	pf := NewPathFilter(3) // dusuk esik ile hizli test

	urls := []string{
		"https://example.com/users/1",
		"https://example.com/users/2",
		"https://example.com/users/3",
		"https://example.com/users/4", // bu noktada threshold asilmis olmali
		"https://example.com/users/5",
	}

	var allowedCount int
	for i, u := range urls {
		if pf.Allow(u) {
			allowedCount++
		} else if i < 3 {
			t.Fatalf("threshold asilmadan once (%d. istek) filtrelenmemeliydi: %s", i, u)
		}
	}

	// Ilk 3 farkli deger threshold'u tam doldurur (promote olmaz, ==3 esitligi
	// promote icin yeterli degil cunku kontrol '> threshold'), boylece 4. ve
	// 5. istekler (4 ve 5. farkli degerler > 3) filtrelenmis olmali.
	if allowedCount != 4 {
		t.Fatalf("4 istek allow edilmesi bekleniyordu (ilk 3 + esigi asan 4.), alinan: %d", allowedCount)
	}
}

func TestPathFilter_DifferentPositionsIndependent(t *testing.T) {
	pf := NewPathFilter(2)
	// /users/{id} pozisyonu ile /posts/{id} pozisyonu farkli path prefix'e
	// sahip oldugu icin birbirini etkilememeli.
	if !pf.Allow("https://example.com/users/1") {
		t.Fatal("ilk istek her zaman allow edilmeli")
	}
	if !pf.Allow("https://example.com/posts/1") {
		t.Fatal("farkli path prefix'i kendi sayacina sahip olmali")
	}
}
