<div align="center">

```
 ____   _____  __      __ _   _  _____   __
|  _ \ / _ \ \/ /  \  / /| | | ||  __ \ / /
| |_) | | | \  /    \/  / | | | || |  | | |
|  _ <| |_| /  \    /\  /  | |_| || |__| | |
|_.__/ \___/_/\_\  /_/\_\   \___/ |_____/|_|
```

### Pasif + Aktif URL Keşif Aracı

**wayback · commoncrawl · alienvault · otx · urlscan · crt.sh · aktif crawl — hepsi tek binary'de**

[![Go Version](https://img.shields.io/badge/Go-1.21%2B-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)](go.mod)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

</div>

---

## Bu araç ne yapar?

`bovurl`, recon/bug-bounty/pentest çalışmalarında ayrı ayrı kullanılan araçların
(katana, urlfinder, gau) yaptığı işi **tek bir pipeline'da** birleştirir:

1. **Pasif toplama** — hedefe hiç istek atmadan, 6 farklı kaynaktan (wayback,
   commoncrawl, alienvault, otx, urlscan, crt.sh) URL ve subdomain toplar.
2. **Normalize + dedup** — toplanan URL'leri tekilleştirir (query param sırası,
   trailing slash, fragment gibi kozmetik farkları eler).
3. **Aktif crawl** — pasif kaynaklardan bulunan her URL'i seed alıp gerçek HTTP
   istekleriyle tarar: HTML linklerini takip eder, JS dosyalarından endpoint
   çıkarır, form alanlarını raporlar.
4. **Tekrar dedup + streaming output** — sonuçları JSONL veya düz metin olarak
   akış halinde (bellekte biriktirmeden) yazar.

Tamamen **Go standart kütüphanesi** ile yazılmıştır — **hiçbir harici
bağımlılık yoktur**. `go build` dışında hiçbir şeye ihtiyaç duymaz, offline
ortamlarda bile derlenebilir.

---

## Neden bovurl?

| | katana | urlfinder / gau | **bovurl** |
|---|:---:|:---:|:---:|
| Pasif kaynak toplama (wayback/commoncrawl/alienvault/otx/urlscan) | ❌ (kaldırıldı) | ✅ | ✅ |
| crt.sh subdomain keşfi | ❌ | ❌ | ✅ |
| Aktif crawl + JS parsing + form extraction | ✅ | ❌ | ✅ |
| Pasif → aktif otomatik zincirleme | ❌ (manuel pipe gerekir) | ❌ | ✅ (tek komut) |
| Harici bağımlılık | çok | çok | **sıfır** |
| Benzer-path filtreleme (`-fsu`) | ✅ | ❌ | ✅ |

Kısacası: `urlfinder | katana` zincirini kurmak yerine tek komutla,
tek binary ile aynı sonucu (ve fazlasını) alırsınız.

---

## Kurulum

```bash
git clone https://github.com/<kullanici-adiniz>/bovurl.git
cd bovurl
go build -o bovurl ./cmd/bovurl
```

Gereksinim: **Go 1.21+**. Başka hiçbir şey gerekmez.

`go install` ile de kurulabilir (repo yayınlandıktan sonra):

```bash
go install github.com/<kullanici-adiniz>/bovurl/cmd/bovurl@latest
```

---

## Hızlı Başlangıç

```bash
# Tam pipeline: pasif toplama + aktif crawl
./bovurl -d example.com -o allurls.txt

# JSONL çıktı (kaynak, status code, form bilgisi dahil)
./bovurl -d example.com -j -o allurls.jsonl

# Sadece belirli pasif kaynaklar
./bovurl -d example.com -s wayback,commoncrawl,crtsh -o out.txt

# Sadece pasif toplama - hedefe hiç istek atmaz, çok hızlı
./bovurl -d example.com -passive-only -o passive.txt

# Birden fazla domain (satır satır dosyadan)
./bovurl -list domains.txt -o allurls.txt

# Sadece aktif crawl - kendi URL listenizle (katana -list gibi)
cat myurls.txt | ./bovurl -active-only -depth 5 -o out.txt

# Benzer path'leri filtrele (/users/123, /users/456 -> tek pattern)
./bovurl -d example.com -fsu -fst 8 -o out.txt

# urlscan.io API key ile daha yüksek rate limit
URLSCAN_API_KEY=xxx ./bovurl -d example.com -o out.txt
```

---

## Örnek Çıktı (JSONL)

```json
{"url":"https://example.com/api/v1/users","source":"active","domain":"example.com","timestamp":"2026-08-02T14:00:00Z","status_code":200,"depth":2}
{"url":"https://example.com/login","source":"active-form","is_form":true,"form_fields":["username","password","csrf_token"]}
{"url":"https://web.example.com/old-page","source":"wayback","domain":"example.com","timestamp":"2026-08-02T14:00:01Z"}
```

---

## Tüm Flag'ler

| Flag | Açıklama | Varsayılan |
|---|---|---|
| `-d` | Hedef domain | - |
| `-list` | Domain listesi dosyası (satır başına bir domain) | - |
| `-o` | Çıktı dosyası | stdout |
| `-j` | JSONL formatı | `false` |
| `-s` | Dahil edilecek pasif kaynaklar (virgülle) | hepsi |
| `-es` | Hariç tutulacak pasif kaynaklar | - |
| `-passive-only` | Sadece pasif toplama | `false` |
| `-active-only` | Sadece aktif crawl (seed'ler stdin'den) | `false` |
| `-depth` | Aktif crawl azami derinlik | `3` |
| `-c` | Aktif crawl concurrency | `10` |
| `-jc` | JS dosyalarından endpoint çıkar | `true` |
| `-fx` | Form alanlarını çıkar | `true` |
| `-ef` | Atlanacak dosya uzantıları | `png,jpg,jpeg,gif,svg,css,woff,woff2,ico` |
| `-fsu` | Benzer path'leri filtrele | `false` |
| `-fst` | `-fsu` için eşik değeri | `10` |
| `-rl` | Pasif kaynak başına saniyede istek | `5` |
| `-timeout` | İstek başına saniye | `15` |
| `-max-time` | Tüm tarama için dakika üst sınırı | `0` (sınırsız) |
| `-urlscan-key` | urlscan.io API key (env `URLSCAN_API_KEY` de okunur) | - |
| `-nc` | Renksiz çıktı | `false` |
| `-silent` | Sadece URL'ler, banner/log yok | `false` |

---

## Mimari

```
┌─────────────────────────────────────────────────────────┐
│  PASİF KATMAN (paralel, fan-in)                          │
│  wayback · commoncrawl · alienvault · otx · urlscan · crtsh│
└───────────────────────┬───────────────────────────────────┘
                         ▼
              Normalize + Dedup (streaming)
                         ▼
┌─────────────────────────────────────────────────────────┐
│  AKTİF KATMAN (katana-tarzı BFS)                          │
│  HTML link takibi · JS endpoint çıkarma · form extraction │
└───────────────────────┬───────────────────────────────────┘
                         ▼
              Normalize + Dedup (streaming)
                         ▼
              Output (JSONL / düz metin)
```

Her pasif kaynak `Provider` interface'ini implement eder — yeni bir kaynak
eklemek `internal/provider/` altına yeni bir dosya + `main.go`'daki
`allProviderFactories` haritasına bir satır eklemek kadar basittir.

Detaylı mimari açıklaması için her paketin (`internal/*`) başındaki yorum
bloklarına bakın.

---

## Engellenme Sorunu Çözüldü: Adaptif Hız + Gerçekçi Header'lar

Aktif crawl fazı artık:
- **Host-bazlı adaptif rate limiting** — her host kendi hızına sahip; 400/403/429 veya bağlantı hatası görülünce o hostun hızı otomatik yarıya iner, ardışık başarılı isteklerde kademeli olarak tekrar hızlanır. Ardışık 3 blok sinyalinde terminale uyarı basılır.
- **Gerçekçi tarayıcı header seti** (Accept, Accept-Language, Sec-Fetch-*, Connection, Upgrade-Insecure-Requests) — çoğu WAF/bot-koruması, rate-limit'e bile gerek kalmadan "eksik header = bariz script" sinyaliyle ilk istekte engelliyor; bu artık varsayılan olarak gönderiliyor.
- `-ua` ile özel User-Agent verilebilir (varsayılan gerçekçi bir Chrome UA'sıdır).

**Sınırlama**: Bu HTTP header seviyesinde bir düzeltmedir — TLS ClientHello imzası (JA3/JA4) hâlâ Go'nun `net/http`'ine ait olduğundan, JA3 tabanlı gelişmiş korumaları (Cloudflare/Akamai gibi) yine de ayırt edebilir. Bunun çözümü IP/kimlik gizlemek (proxychains/VPN) değildir — doğru çözüm ya tarama IP'nizi hedefte allowlist'e aldırmak ya da ileri seviyede özel bir TLS-fingerprint kütüphanesi kullanmaktır.

## Test

```bash
go test ./... -v
```

Network gerektirmeyen unit testler `normalize` ve `ratelimit` paketlerinde
mevcuttur (dedup mantığı, URL normalizasyonu, rate limiter davranışı,
path-filter promotion mantığı).

---

## Bilinen Sınırlamalar

- **HTML parsing regex tabanlı** — `golang.org/x/net/html` gibi tam bir DOM
  parser değildir; çok bozuk/atipik HTML'de bazı linkler kaçabilir.
- **Rate limiter global** (host-bazlı değil) — çok domainli taramalarda tüm
  domainler aynı limiti paylaşır.
- **Resume/checkpoint yok** — uzun süren taramalar kesintiye uğrarsa baştan
  başlar.

Katkı yapmak isteyenler için bunlar iyi birer başlangıç noktası 🙂

---

## Katkıda Bulunma

Pull request'lere açığız. Yeni bir pasif kaynak eklemek için:

1. `internal/provider/` altına `Provider` interface'ini implement eden yeni bir dosya ekleyin.
2. `cmd/bovurl/main.go` içindeki `allProviderFactories` haritasına bir satır ekleyin.
3. Mümkünse `httptest.NewServer` ile mock'lanmış bir unit test ekleyin.

---

## Sorumlu Kullanım ⚠️

Bu araç **sadece yazılı izniniz olan hedeflerde** kullanılmak üzere
tasarlanmıştır. wayback, crt.sh, commoncrawl gibi üçüncü taraf servislerin
kullanım şartlarına uyun — agresif paralellik IP banına yol açabilir ve
paylaşılan altyapıyı diğer kullanıcılar için de yavaşlatabilir.

Geliştiriciler bu aracın kötüye kullanımından sorumlu değildir.

---

## Lisans

[MIT](LICENSE)
